package npm

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/darkraise/darkpanel/internal/core"
)

// ctlListParallel bounds the agent reads npm.instance.list runs at once.
const ctlListParallel = 8

// instanceList declares npm.instance.list: the NPM instance status of every host.
func (n nm) instanceList() core.ActionDef {
	return core.DefineRead[InstanceListInput, InstancesOutput](core.ActionSpec{
		Name:    ID + ".instance.list",
		Summary: "List the Nginx Proxy Manager instance status of every host.",
		Version: 1,
		Side:    core.SideController,
	}, ctlInstanceListReader{})
}

// ctlInstanceListReader is the handler of npm.instance.list.
type ctlInstanceListReader struct{}

// ctlHost is one row of core_hosts.
type ctlHost struct{ ID, Name string }

// Read lists the hosts whose status could be read, in id order, and names the hosts whose status could not be read
// in Unreadable, in id order. A cancelled request fails the list, because a list thinned by the cancellation would
// claim that nothing is exposed.
func (ctlInstanceListReader) Read(ctx context.Context, env core.Env, _ InstanceListInput) (InstancesOutput, error) {
	hosts, err := ctlListHosts(ctx, env.DB.Read())
	if err != nil {
		return InstancesOutput{}, err
	}
	statuses := make([]InstanceStatus, len(hosts))
	read := make([]bool, len(hosts))
	sem := make(chan struct{}, ctlListParallel)
	var wg sync.WaitGroup
fanOut:
	for i, h := range hosts {
		if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break fanOut
		}
		// A free slot and a cancelled context can both be ready, and select may have chosen the slot.
		if ctx.Err() != nil {
			<-sem
			break
		}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			st, err := ctlInstanceStatus(ctx, env, h.ID)
			if err != nil {
				if ctx.Err() == nil {
					env.Log.Debug("npm: the instance list leaves out a host whose status cannot be read", "host", h.ID, "error", err)
				}
				return
			}
			statuses[i], read[i] = st, true
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return InstancesOutput{}, err
	}
	out := InstancesOutput{Instances: []HostInstance{}, Unreadable: []string{}}
	for i, h := range hosts {
		if read[i] {
			out.Instances = append(out.Instances, HostInstance{HostID: h.ID, HostName: h.Name, Status: statuses[i]})
		} else {
			out.Unreadable = append(out.Unreadable, h.ID)
		}
	}
	return out, nil
}

// ctlListHosts reads every host by id.
func ctlListHosts(ctx context.Context, db *sql.DB) ([]ctlHost, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name FROM core_hosts ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("npm: read the hosts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var hosts []ctlHost
	for rows.Next() {
		var h ctlHost
		if err := rows.Scan(&h.ID, &h.Name); err != nil {
			return nil, fmt.Errorf("npm: read the hosts: %w", err)
		}
		hosts = append(hosts, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("npm: read the hosts: %w", err)
	}
	return hosts, nil
}
