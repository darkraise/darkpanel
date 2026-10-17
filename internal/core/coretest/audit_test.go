package coretest

import (
	"context"
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestMemJournal(t *testing.T) {
	var j MemJournal
	ctx := context.Background()
	if err := j.Append(ctx, nil, core.JournalEntry{Action: "demo.item.add", Phase: "apply"}); err != nil {
		t.Fatal(err)
	}
	j.Err = errors.New("disk full")
	if err := j.Append(ctx, nil, core.JournalEntry{Action: "demo.item.add"}); err == nil {
		t.Error("Append with Err set succeeded")
	}
	if got := j.Entries(); len(got) != 1 || got[0].Phase != "apply" {
		t.Errorf("Entries = %+v, want the one successful append", got)
	}
}

func TestMemAudit(t *testing.T) {
	var a MemAudit
	ctx := context.Background()
	var _ core.Audit = &a
	if err := a.Append(ctx, nil, core.JournalEntry{Actor: "user:1", Action: "core.token.create", Phase: "apply"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Record(ctx, core.AuditEntry{Actor: "user:1", Phase: "login"}); err != nil {
		t.Fatal(err)
	}
	batch := []core.JournalEntry{{Seq: 1, Actor: "user:1", Action: "demo.item.add"}, {Seq: 2, Actor: "user:2", Action: "demo.item.add"}}
	for range 2 {
		if err := a.Ingest(ctx, "H1", batch); err != nil {
			t.Fatal(err)
		}
	}

	all, next, err := a.List(ctx, core.AuditQuery{})
	if err != nil || len(all) != 4 || next != "" {
		t.Fatalf("List = %d entries, next %q, %v; want 4 (ingest deduplicated), no next", len(all), next, err)
	}
	if all[0].Seq != 2 || all[0].HostID != "H1" || all[3].Action != "core.token.create" || all[3].HostID != "" {
		t.Errorf("List order or fields wrong: %+v", all)
	}

	page, next, _ := a.List(ctx, core.AuditQuery{Limit: 3})
	if len(page) != 3 || next != page[2].ID {
		t.Fatalf("page 1 = %d entries, next %q; want 3 and the last id", len(page), next)
	}
	rest, next, _ := a.List(ctx, core.AuditQuery{Limit: 3, Before: next})
	if len(rest) != 1 || next != "" || rest[0].Action != "core.token.create" {
		t.Errorf("page 2 = %+v, next %q; want the oldest entry and no next", rest, next)
	}

	byActor, _, _ := a.List(ctx, core.AuditQuery{Actor: "user:2"})
	byHost, _, _ := a.List(ctx, core.AuditQuery{HostID: "H1", Action: "demo.item.add"})
	if len(byActor) != 1 || len(byHost) != 2 {
		t.Errorf("filters: actor %d entries, host+action %d; want 1 and 2", len(byActor), len(byHost))
	}
}
