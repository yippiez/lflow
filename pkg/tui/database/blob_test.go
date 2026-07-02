package database

import (
	"bytes"
	"testing"

	"github.com/lflow/lflow/pkg/utils/assert"
)

func TestBlobCRUD(t *testing.T) {
	db := InitTestMemoryDB(t)
	defer db.Close()

	data := []byte{0x89, 'P', 'N', 'G', 1, 2, 3}
	if err := PutBlob(db, Blob{UUID: "n1", Mime: "image/png", Bytes: data, W: 320, H: 180}); err != nil {
		t.Fatal(err)
	}

	got, ok, err := GetBlob(db, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("blob n1 should exist")
	}
	if !bytes.Equal(got.Bytes, data) {
		t.Fatalf("bytes mismatch: %v", got.Bytes)
	}
	assert.Equal(t, got.W, 320, "width mismatch")
	assert.Equal(t, got.H, 180, "height mismatch")
	assert.Equal(t, got.Mime, "image/png", "mime mismatch")

	// Put again overwrites (ON CONFLICT).
	if err := PutBlob(db, Blob{UUID: "n1", Mime: "image/png", Bytes: []byte{9}, W: 1, H: 1}); err != nil {
		t.Fatal(err)
	}
	got, _, _ = GetBlob(db, "n1")
	assert.Equal(t, got.W, 1, "overwrite width mismatch")

	// Missing blob → ok=false, no error.
	if _, ok, err := GetBlob(db, "nope"); err != nil || ok {
		t.Fatalf("missing blob: ok=%v err=%v", ok, err)
	}

	if err := DeleteBlob(db, "n1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := GetBlob(db, "n1"); ok {
		t.Fatal("deleted blob should be gone")
	}
}

// TestGCBlobs drops blobs whose node is absent or tombstoned, keeps live ones.
func TestGCBlobs(t *testing.T) {
	db := InitTestMemoryDB(t)
	defer db.Close()

	mustInsert(t, db, Node{UUID: "live", Name: "kept", Type: TypeImage})
	mustInsert(t, db, Node{UUID: "dead", Name: "gone", Type: TypeImage, Deleted: true})
	for _, u := range []string{"live", "dead", "orphan"} {
		if err := PutBlob(db, Blob{UUID: u, Mime: "image/png", Bytes: []byte{1}}); err != nil {
			t.Fatal(err)
		}
	}

	if err := GCBlobs(db); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := GetBlob(db, "live"); !ok {
		t.Error("live node's blob should survive GC")
	}
	if _, ok, _ := GetBlob(db, "dead"); ok {
		t.Error("tombstoned node's blob should be GC'd")
	}
	if _, ok, _ := GetBlob(db, "orphan"); ok {
		t.Error("orphan blob should be GC'd")
	}
}
