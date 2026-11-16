package repository

import (
	"testing"
	"testing/fstest"
)

// TestContractsFS: the contracts in memory are a well-behaved fs.FS.
func TestContractsFS(t *testing.T) {
	fsys := contractsFS{"database.schema.json": []byte("{}"), "s3.schema.json": []byte(`{"type": "object"}`)}
	if err := fstest.TestFS(fsys, "database.schema.json", "s3.schema.json"); err != nil {
		t.Fatal(err)
	}
}
