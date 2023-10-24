package archive

import (
	"os"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
)

func OpenDB(t *testing.T) *leveldb.DB {
    db, err := leveldb.OpenFile("./testdb", nil)
    if err != nil {
        t.Fatal(err)
    }

    return db
}

func CleanUp(t *testing.T, db *leveldb.DB) {
    err := db.Close()
    if err != nil {
        t.Fatalf("Failed test db clean up: %s", err.Error())
    }

    err = os.RemoveAll("./testdb")
    if err != nil {
        t.Fatalf("Failed test db clean up: %s", err.Error())
    }
}


func TestGetFid(t *testing.T) {
    db := OpenDB(t)
    defer CleanUp(t, db)

    archive := DoubleRefArchiveDB{db: db}

    key := []byte("cid0")
    value := []byte("fid0")
    err := db.Put(key, value, nil)
    if err != nil {
        t.Fatal(err)
    }

    v, err := archive.GetFid(string(key))
    if err != nil {
        t.Fatalf("%s: %s", string(key), err.Error())
    }
    
    if v != string(value) {
        t.Errorf("%s: %s, expected %s", string(key), string(value), string(v))
    }
}

func TestGetContracts(t *testing.T) {
    db := OpenDB(t)
    defer CleanUp(t, db)
    
    archive := DoubleRefArchiveDB{db: db}

    k0 := []byte("fid0")
    v0 := []byte("cid0,cid1,cid2")

    err := db.Put(k0, v0, nil)
    if err != nil {
        t.Fatal(err)
    }
    
    cids, err := archive.GetContracts(string(k0))
    if err != nil {
        t.Fatal(err)
    }

    for _, c := range cids {
        switch string(c) {
        case "cid0":
            continue
        case "cid1":
            continue
        case "cid2":
            continue
        default:
            t.Errorf("%s: %v, expected [cid0, cid1, cid2]", k0, cids)
        }
    }
}


