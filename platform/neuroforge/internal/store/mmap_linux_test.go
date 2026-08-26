//go:build linux

package store

import (
	"path/filepath"
	"strings"
	"testing"

	"neuroforge/internal/core"
)

func TestSealedSegmentUsesMmapOnLinux(t *testing.T) {
	ss, err := openSegmentStore(filepath.Join(t.TempDir(), "segments"), 1<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	big := strings.Repeat("x", 700<<10)
	m1 := core.Memory{ID: "mmap1", Text: big, Vector: []float32{1, 0}}
	m2 := core.Memory{ID: "mmap2", Text: big, Vector: []float32{0, 1}}
	if err := ss.AppendUpsert(1, []core.Memory{m1}); err != nil {
		t.Fatal(err)
	}
	if err := ss.AppendUpsert(2, []core.Memory{m2}); err != nil {
		t.Fatal(err)
	}
	if ss.Stats().Segments < 2 {
		t.Fatalf("expected rotation: %#v", ss.Stats())
	}
	got, found, deleted, err := ss.Get("mmap1")
	if err != nil || !found || deleted || len(got.Text) != len(big) {
		t.Fatalf("bad mmap read: found=%v deleted=%v err=%v", found, deleted, err)
	}
	if ss.Stats().MmapSegments < 1 {
		t.Fatalf("expected sealed segment mmap, stats=%#v", ss.Stats())
	}
}
