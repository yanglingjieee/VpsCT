package conntail

import (
	"testing"
	"time"
)

func TestSharedListenerEventsBelongToTheUser(t *testing.T) {
	now := time.Now()
	tl := New("")
	tl.Allowed = func(id int64) bool { return id == 20 } // only user 20 is recorded
	tl.Shared = func(id int64) bool { return id == 5 }
	feed := func(line string) {
		if p, ok := parseLine(line, now); ok && tl.wanted(p) {
			tl.ingest(p, now)
		}
	}
	feed(`+0000 2026-10-06 12:00:00 INFO [111 0ms] inbound/vless[node-5]: inbound connection from 198.51.100.7:50000`)
	feed(`+0000 2026-10-06 12:00:00 INFO [222 3ms] inbound/vless[node-5]: [n20] inbound connection to example.com:443`)
	feed(`+0000 2026-10-06 12:00:01 INFO [333 0ms] inbound/vless[node-5]: inbound connection from 198.51.100.8:50001`)
	feed(`+0000 2026-10-06 12:00:01 INFO [444 3ms] inbound/vless[node-5]: [n21] inbound connection to secret.example:443`)
	feed(`+0000 2026-10-06 12:00:02 INFO [555 3ms] inbound/vless[node-5]: [n5] inbound connection to owner.example:443`)
	evs := tl.Take(10)
	if len(evs) != 1 || evs[0].NodeID != 20 || evs[0].DestHost != "example.com" || evs[0].SrcHost != "198.51.100.7" {
		t.Fatalf("only user 20's connection, attributed to user 20 with its source: %+v", evs)
	}
}
