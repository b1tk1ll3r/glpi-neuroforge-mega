package store

import (
	"bufio"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"neuroforge/internal/core"
)

func TestVectorJournalV2SQARRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vector-journal.nfv")
	j, err := openVectorJournal(path, vectorJournalOptions{
		Compression: "sqar-auto", BlockVectors: 128, MinBlockBytes: 1, MinSavingsPct: 0.01,
	})
	if err != nil {
		t.Fatal(err)
	}
	mems := make([]core.Memory, 64)
	for r := range mems {
		v := make([]float32, 768)
		for i := range v {
			v[i] = float32(math.Sin(float64(i)/19+float64(r)/31) * 0.15)
		}
		mems[r] = core.Memory{ID: NewID("vec"), Vector: v, VectorDim: len(v)}
	}
	if err := j.AppendNew(7, mems); err != nil {
		t.Fatal(err)
	}
	st := j.Stats()
	if st.Format != "NFVJ2" || st.Records != len(mems) {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if st.SQARBlocks == 0 || st.VectorStoredBytes >= st.VectorRawBytes {
		t.Fatalf("expected useful SQAR block compression: %+v", st)
	}
	t.Logf("SQAR vector block stats: %+v", st)
	seen := 0
	if err := j.Iterate(768, func(id string, v []float32) error {
		want := mems[seen]
		if id != want.ID || len(v) != len(want.Vector) {
			t.Fatalf("record %d mismatch id/dim", seen)
		}
		for i := range v {
			if math.Float32bits(v[i]) != math.Float32bits(want.Vector[i]) {
				t.Fatalf("record %d vector[%d] mismatch", seen, i)
			}
		}
		seen++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != len(mems) {
		t.Fatalf("iterated %d vectors, want %d", seen, len(mems))
	}
}

func TestVectorJournalUpgradesV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vector-journal.nfv")
	legacy := []core.Memory{
		{ID: "legacy-a", Vector: []float32{1, 2, 3, 4}},
		{ID: "legacy-b", Vector: []float32{5, 6, 7, 8}},
	}
	writeLegacyVectorJournal(t, path, 11, legacy)
	j, err := openVectorJournal(path, vectorJournalOptions{Compression: "off", BlockVectors: 128})
	if err != nil {
		t.Fatal(err)
	}
	if st := j.Stats(); st.Format != "NFVJ2" || st.Records != 2 {
		t.Fatalf("V1 was not upgraded: %+v", st)
	}
	var got []string
	if err := j.Iterate(4, func(id string, v []float32) error {
		got = append(got, id)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "legacy-a" || got[1] != "legacy-b" {
		t.Fatalf("unexpected upgraded records: %v", got)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < len(vectorJournalMagicV2) || string(b[:len(vectorJournalMagicV2)]) != vectorJournalMagicV2 {
		t.Fatal("upgraded journal does not have NFVJ2 header")
	}
}

func writeLegacyVectorJournal(t *testing.T, path string, revision uint64, memories []core.Memory) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	bw := bufio.NewWriter(f)
	if _, err := bw.WriteString(vectorJournalMagicV1); err != nil {
		t.Fatal(err)
	}
	var b4 [4]byte
	var b8 [8]byte
	var b2 [2]byte
	for _, m := range memories {
		payload := 12 + len(m.ID) + len(m.Vector)*4
		binary.LittleEndian.PutUint32(b4[:], uint32(payload))
		_, _ = bw.Write(b4[:])
		binary.LittleEndian.PutUint64(b8[:], revision)
		_, _ = bw.Write(b8[:])
		binary.LittleEndian.PutUint16(b2[:], uint16(len(m.ID)))
		_, _ = bw.Write(b2[:])
		binary.LittleEndian.PutUint16(b2[:], uint16(len(m.Vector)))
		_, _ = bw.Write(b2[:])
		_, _ = bw.WriteString(m.ID)
		for _, x := range m.Vector {
			binary.LittleEndian.PutUint32(b4[:], math.Float32bits(x))
			_, _ = bw.Write(b4[:])
		}
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
