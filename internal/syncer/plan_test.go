package syncer

import (
	"strings"
	"testing"
	"time"

	"github.com/CosmicKittu/Syncme/internal/index"
)

func e(h string, m int64) index.Entry { return index.Entry{Size: 1, MTime: m, Hash: strings.Repeat(h, 64)} }

type M = map[string]index.Entry

func kinds(acts []Action) map[string]Kind {
	out := map[string]Kind{}
	for _, a := range acts {
		out[a.Path] = a.Kind
	}
	return out
}

func TestPlan(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	base := M{"same": e("a", 1), "pcedit": e("a", 1), "phoneedit": e("a", 1), "pcdel": e("a", 1), "phonedel": e("a", 1),
		"both": e("a", 1), "bothdel": e("a", 1), "deledit": e("a", 1), "editdel": e("a", 1), "conv": e("a", 1)}
	local := M{"same": e("a", 1), "pcedit": e("b", 2), "phoneedit": e("a", 1), "phonedel": e("a", 1),
		"both": e("b", 5), "editdel": e("b", 2), "newpc": e("c", 1), "newboth": e("d", 1), "newconf": e("e", 9), "conv": e("z", 3)}
	remote := M{"same": e("a", 1), "pcedit": e("a", 1), "phoneedit": e("b", 2), "pcdel": e("a", 1),
		"both": e("c", 6), "deledit": e("b", 2), "newphone": e("c", 1), "newboth": e("d", 1), "newconf": e("f", 2), "conv": e("z", 3)}
	got := kinds(Plan(base, local, remote, now, "pc", "phone"))
	want := map[string]Kind{
		"pcedit": Push, "phoneedit": Pull, "pcdel": DelRemote, "phonedel": DelLocal,
		"both": ConflictKeepRemote, "bothdel": BaseRemove, "deledit": Pull, "editdel": Push,
		"newpc": Push, "newphone": Pull, "newboth": BaseSet, "newconf": ConflictKeepLocal, "conv": BaseSet,
	}
	for p, k := range want {
		if got[p] != k {
			t.Errorf("%s: got %v want %v", p, got[p], k)
		}
	}
	if _, ok := got["same"]; ok {
		t.Errorf("unchanged file should produce no action")
	}
	if len(got) != len(want) {
		t.Errorf("unexpected actions: %v", got)
	}
}

func TestConflictName(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	local := M{"d/a.txt": e("a", 1), "d/a.sync-conflict-20260102-030405-pc.txt": e("x", 1)}
	remote := M{"d/a.txt": e("b", 9)}
	acts := Plan(M{}, local, remote, now, "pc", "phone")
	for _, a := range acts {
		if a.Path == "d/a.txt" {
			if a.ConflictName != "d/a.sync-conflict-20260102-030405-pc-2.txt" {
				t.Fatalf("bad conflict name %q", a.ConflictName)
			}
			return
		}
	}
	t.Fatal("no conflict action")
}

func TestValidPath(t *testing.T) {
	for _, p := range []string{"", "/etc/passwd", "../x", "a/../b", "a//b", "a/./b", ".syncme-trash/x", "a\\b", "a\x00"} {
		if index.ValidPath(p) {
			t.Errorf("%q should be invalid", p)
		}
	}
	for _, p := range []string{"a", "a/b.txt", ".hidden", "dir with space/ü.png"} {
		if !index.ValidPath(p) {
			t.Errorf("%q should be valid", p)
		}
	}
}
