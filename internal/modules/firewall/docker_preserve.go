package firewall

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/firewall/dockerfw"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
)

// foreignRecord is what a transition that flushes DOCKER-USER needs to give
// the foreign rules back: where they sat in each family and the lines to
// load around them.
type foreignRecord struct {
	Layouts map[fwstate.Family]dockerfw.Layout `json:"layouts"`
	Block   map[fwstate.Family][]string        `json:"block"` // the DOCKER-USER lines to reload; nil: -j RETURN
	// Known are the after files whose block lines count as Darkpanel's,
	// kept because a repeat may find the files on disk without the block.
	Known [][]byte `json:"known,omitempty"`
}

// preserveFamilies are the families whose DOCKER-USER is kept, in the order
// they are loaded.
var preserveFamilies = []fwstate.Family{fwstate.V4, fwstate.V6}

// putForeign stores rec as owner's record, created at at, replacing an
// earlier one, and commits it before returning.
func putForeign(ctx context.Context, db *store.DB, owner string, rec foreignRecord, at time.Time) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("firewall: record the foreign DOCKER-USER rules of %s: %w", owner, err)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"INSERT OR REPLACE INTO firewall_docker_foreign (owner, record, created_at) VALUES (?, ?, ?)",
			owner, string(data), formatSnapshotTime(at))
		return err
	})
	if err != nil {
		return fmt.Errorf("firewall: record the foreign DOCKER-USER rules of %s: %w", owner, err)
	}
	return nil
}

// foreignOf returns owner's record; ok is false when there is none.
func foreignOf(ctx context.Context, db *store.DB, owner string) (rec foreignRecord, ok bool, err error) {
	var data string
	err = db.Read().QueryRowContext(ctx, "SELECT record FROM firewall_docker_foreign WHERE owner = ?", owner).Scan(&data)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return foreignRecord{}, false, nil
	case err != nil:
		return foreignRecord{}, false, fmt.Errorf("firewall: read the foreign DOCKER-USER rules of %s: %w", owner, err)
	}
	if err := json.Unmarshal([]byte(data), &rec); err != nil {
		return foreignRecord{}, false, fmt.Errorf("firewall: decode the foreign DOCKER-USER rules of %s: %w", owner, err)
	}
	return rec, true, nil
}

// deleteForeign deletes owner's record; an absent owner is not an error.
func deleteForeign(ctx context.Context, db *store.DB, owner string) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM firewall_docker_foreign WHERE owner = ?", owner)
		return err
	})
	if err != nil {
		return fmt.Errorf("firewall: clear the foreign DOCKER-USER rules of %s: %w", owner, err)
	}
	return nil
}

// adoptForeign moves the record of from into to's, merged with any record to
// holds, in one transaction, and reports whether to holds a record
// afterwards. The adopted rules come first: they were flushed earlier.
func adoptForeign(ctx context.Context, db *store.DB, from, to string, at time.Time) (bool, error) {
	mine, held, err := foreignOf(ctx, db, to)
	if err != nil {
		return false, err
	}
	rec, ok, err := foreignOf(ctx, db, from)
	if err != nil || !ok {
		return held, err
	}
	if held {
		rec.Layouts = mergeLayouts(rec.Layouts, mine.Layouts)
		rec.Known = append(rec.Known, mine.Known...)
		rec.Block = mine.Block
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return false, fmt.Errorf("firewall: move the foreign DOCKER-USER rules of %s to %s: %w", from, to, err)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"INSERT OR REPLACE INTO firewall_docker_foreign (owner, record, created_at) VALUES (?, ?, ?)",
			to, string(data), formatSnapshotTime(at)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM firewall_docker_foreign WHERE owner = ?", from)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("firewall: move the foreign DOCKER-USER rules of %s to %s: %w", from, to, err)
	}
	return true, nil
}

// blockUserRules returns the DOCKER-USER rules of the Docker block of each
// file, in file order; a file without a block adds none.
func blockUserRules(files [][]byte) ([]netfilter.Rule, error) {
	var out []netfilter.Rule
	for _, file := range files {
		block, ok, err := dockerfw.BlockOf(file)
		if err != nil {
			return nil, fmt.Errorf("firewall: read the Docker block: %w", err)
		}
		if !ok {
			continue
		}
		rs, err := dockerfw.BlockRules(block)
		if err != nil {
			return nil, fmt.Errorf("firewall: read the Docker block: %w", err)
		}
		if c := chainOf(rs, "filter", dockerfw.ChainDockerUser); c != nil {
			out = append(out, c.Rules...)
		}
	}
	return out, nil
}

// keepForeign runs transition, which flushes DOCKER-USER, and then reloads
// each family's DOCKER-USER with the lines next and the foreign rules it
// held in their places. The foreign rules are recorded for owner before
// transition runs, so that a failed or cut-off attempt leaves them for the
// next attempt or finishForeign. known are the files whose block lines count
// as Darkpanel's. interleaved reports that a foreign rule sat between block
// lines and was loaded before the block.
func (f fw) keepForeign(ctx context.Context, env core.Env, owner string, known [][]byte, next map[fwstate.Family][]string, transition func() error) (interleaved bool, err error) {
	cur, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return false, fmt.Errorf("firewall: read DOCKER-USER: %w", err)
	}
	earlier, ok, err := foreignOf(ctx, env.DB, owner)
	if err != nil {
		return false, err
	}
	files := slices.Clone(known)
	for _, file := range earlier.Known {
		if !slices.ContainsFunc(files, func(k []byte) bool { return bytes.Equal(k, file) }) {
			files = append(files, file)
		}
	}
	rules, err := blockUserRules(files)
	if err != nil {
		return false, err
	}
	layouts := map[fwstate.Family]dockerfw.Layout{}
	for _, fam := range preserveFamilies {
		rs := cur.V4
		if fam == fwstate.V6 {
			rs = cur.V6
		}
		if c := chainOf(rs, "filter", dockerfw.ChainDockerUser); c != nil {
			layouts[fam] = dockerfw.LayoutOf(c, rules, fam)
		}
	}
	if ok {
		layouts = mergeLayouts(earlier.Layouts, layouts)
	}
	rec := foreignRecord{Layouts: layouts, Block: next, Known: files}
	if err := putForeign(ctx, env.DB, owner, rec, env.Clock.Now()); err != nil {
		return false, err
	}
	if err := transition(); err != nil {
		return false, err
	}
	return reloadDockerUser(ctx, env, owner, cur.Variant, rec)
}

// mergeLayouts combines the layouts an earlier attempt recorded with those
// read now. A recorded rule the chain still holds is not repeated, copy for
// copy, and one the earlier attempt's flush removed comes before the newer
// rules. A recorded line the chain now holds only among the block's lines
// is not repeated either.
func mergeLayouts(rec, cur map[fwstate.Family]dockerfw.Layout) map[fwstate.Family]dockerfw.Layout {
	out := map[fwstate.Family]dockerfw.Layout{}
	for _, fam := range preserveFamilies {
		r, inRec := rec[fam]
		c, inCur := cur[fam]
		switch {
		case inRec && inCur:
			held := map[string]int{}
			for _, line := range slices.Concat(c.Before, c.After) {
				held[line]++
			}
			block := map[string]bool{}
			for _, line := range c.Order {
				block[line] = held[line] == 0
			}
			gone := func(recorded, current []string) []string {
				var lines []string
				for _, line := range recorded {
					switch {
					case held[line] > 0:
						held[line]--
					case !block[line]:
						lines = append(lines, line)
					}
				}
				return append(lines, current...)
			}
			l := dockerfw.Layout{Before: gone(r.Before, c.Before), After: gone(r.After, c.After), Interleaved: r.Interleaved || c.Interleaved, Order: c.Order}
			if r.Interleaved {
				l.Order = r.Order
			}
			out[fam] = l
		case inRec:
			out[fam] = r
		case inCur:
			out[fam] = c
		}
	}
	return out
}

// finishForeign completes owner's reload when a record is left, after a
// failed or cut-off keepForeign; with no record it does nothing. The attempt
// may have stopped before its transition flushed DOCKER-USER, so the chain
// can hold foreign rules added after the record was written: each recorded
// family is reloaded with the record merged with the loaded chain, as
// keepForeign merges a record an earlier attempt left.
//
// With keepLoaded, the reload only puts back the rules the record adds: each
// family keeps the non-foreign lines its chain holds now in place of the
// record's block, and a family whose chain already holds every recorded
// rule is not reloaded. That is for a record whose transition is being
// undone, where the record's block is that transition's target: loading it
// over a block that is still loaded would drop the block's DNAT gate, and
// leave published ports unfiltered until the undo loads the block again.
func (f fw) finishForeign(ctx context.Context, env core.Env, owner string, keepLoaded bool) (interleaved bool, err error) {
	rec, ok, err := foreignOf(ctx, env.DB, owner)
	if err != nil || !ok {
		return false, err
	}
	cur, err := f.d.readLoaded(ctx, env)
	if err != nil {
		return false, fmt.Errorf("firewall: read DOCKER-USER: %w", err)
	}
	cfg, err := f.d.readConfig(f.d.root)
	if err != nil {
		return false, fmt.Errorf("firewall: read the configuration to keep foreign DOCKER-USER rules: %w", err)
	}
	rules, err := blockUserRules(rec.Known)
	if err != nil {
		return false, err
	}
	// A loaded block line that only the files now hold would otherwise be
	// kept as foreign, with its jump to a chain a cleanup then cannot delete.
	for _, path := range cleanupFiles {
		if file := cfg.Files[path]; file.Present {
			rs, _ := kfwBlockRules(file.Data)
			rules = append(rules, rs...)
		}
	}
	live := map[fwstate.Family]dockerfw.Layout{}
	chains := map[fwstate.Family]*netfilter.Chain{}
	for _, fam := range preserveFamilies {
		if _, recorded := rec.Layouts[fam]; !recorded {
			continue
		}
		rs := cur.V4
		if fam == fwstate.V6 {
			rs = cur.V6
		}
		c := chainOf(rs, "filter", dockerfw.ChainDockerUser)
		if c == nil {
			continue
		}
		chains[fam] = c
		live[fam] = dockerfw.LayoutOf(c, rules, fam)
	}
	merged := mergeLayouts(rec.Layouts, live)
	if keepLoaded {
		block := map[fwstate.Family][]string{}
		for fam, l := range merged {
			if len(l.Before)+len(l.After) == len(live[fam].Before)+len(live[fam].After) {
				delete(merged, fam)
				continue
			}
			block[fam] = rec.Block[fam]
			if c := chains[fam]; c != nil {
				block[fam] = ownLines(c, rules, fam)
			}
		}
		rec.Block = block
	}
	rec.Layouts = merged
	return reloadDockerUser(ctx, env, owner, cur.Variant, rec)
}

// ownLines returns the save text of the rules of c that are not foreign to
// known, in chain order.
func ownLines(c *netfilter.Chain, known []netfilter.Rule, fam fwstate.Family) []string {
	foreign := map[int]bool{}
	for _, r := range dockerfw.Foreign(c, known, fam) {
		foreign[r.Line] = true
	}
	var out []string
	for _, r := range c.Rules {
		if !foreign[r.Line] {
			out = append(out, r.Text)
		}
	}
	return out
}

// reloadDockerUser loads, IPv4 first, the DOCKER-USER of each family rec
// has a layout for, and deletes owner's record once every family loaded. A
// failed load keeps the record and loads no later family.
func reloadDockerUser(ctx context.Context, env core.Env, owner, variant string, rec foreignRecord) (interleaved bool, err error) {
	for _, fam := range preserveFamilies {
		layout, ok := rec.Layouts[fam]
		if !ok {
			continue
		}
		cmd := core.Cmd{Path: restoreProgram(fam, variant), Args: []string{"-n"}, Stdin: dockerfw.DockerUserInput(rec.Block[fam], layout)}
		res, err := env.Exec.Run(ctx, cmd)
		if err != nil || res.ExitCode != 0 {
			return false, fmt.Errorf("firewall: load DOCKER-USER (%s): %w", fam, runFailure(cmd, res, err))
		}
		if layout.Interleaved {
			interleaved = true
			env.Log.Warn("firewall: foreign DOCKER-USER rules sat between the block's lines; they are loaded before the block",
				"family", fam, "order", layout.Order)
		}
	}
	if err := deleteForeign(ctx, env.DB, owner); err != nil {
		return false, err
	}
	return interleaved, nil
}
