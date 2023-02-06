package utils

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/tendermint/tendermint/libs/log"
	"github.com/wealdtech/go-merkletree"
	"github.com/wealdtech/go-merkletree/sha3"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/spf13/cobra"
	"github.com/syndtr/goleveldb/leveldb"
)

func DownloadFileFromURL(cmd *cobra.Command, url string, fid string, cid string, db *leveldb.DB, logger log.Logger) (string, error) {
	logger.Info(fmt.Sprintf("Getting %s from %s", fid, url))

	resp, err := http.Get(fmt.Sprintf("%s/download/%s", url, fid))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed to find file on network")
	}
	defer resp.Body.Close()

	buff := bytes.NewBuffer([]byte{})
	size, err := io.Copy(buff, resp.Body)
	if err != nil {
		return "", err
	}

	reader := bytes.NewReader(buff.Bytes())

	hashName, err := WriteFileToDisk(cmd, reader, reader, nil, size, logger)
	if err != nil {
		return "", err
	}

	err = SaveToDatabase(hashName, cid, db, logger)
	if err != nil {
		return hashName, err
	}

	clientCtx, err := client.GetClientTxContext(cmd)
	if err != nil {
		return hashName, err
	}

	files := GetStoragePath(clientCtx, fid)

	f, err := os.Open(files)
	if err != nil {
		return hashName, err
	}
	defer f.Close()

	fileInfo, err := f.Readdir(-1)
	if err != nil {
		return hashName, err
	}

	var data [][]byte

	lengthFile := len(fileInfo)

	if lengthFile == 0 {
		return hashName, fmt.Errorf("File not found on this machine")
	}

	for i := 0; i < lengthFile; i++ {

		f, err := os.ReadFile(filepath.Join(files, fmt.Sprintf("%d.jkl", i)))
		if err != nil {
			return hashName, err
		}

		h := sha256.New()
		_, err = io.WriteString(h, fmt.Sprintf("%d%x", i, f))
		if err != nil {
			return hashName, err
		}
		hashName := h.Sum(nil)

		data = append(data, hashName)
	}

	tree, err := merkletree.NewUsing(data, sha3.New512(), false)
	if err != nil {
		return hashName, err
	}

	exportedTree, err := tree.Export()
	if err != nil {
		return hashName, err
	}

	err = db.Put(MakeTreeKey(fid), exportedTree, nil)
	if err != nil {
		return hashName, err
	}

	return hashName, nil
}

func WriteFileToDisk(cmd *cobra.Command, reader io.Reader, file io.ReaderAt, closer io.Closer, size int64, logger log.Logger) (string, error) {
	clientCtx := client.GetClientContextFromCmd(cmd)

	h := sha256.New()
	_, err := io.Copy(h, reader)
	if err != nil {
		return "", err
	}
	hashName := h.Sum(nil)
	fid, err := MakeFid(hashName)
	if err != nil {
		return "", err
	}

	path := GetStoragePath(clientCtx, fid)

	// This is path which we want to store the file
	direrr := os.MkdirAll(path, os.ModePerm)
	if direrr != nil {
		return fid, direrr
	}

	var blocksize int64 = 1024
	var i int64
	for i = 0; i < size; i += blocksize {
		f, err := os.OpenFile(filepath.Join(path, fmt.Sprintf("%d.jkl", i/blocksize)), os.O_WRONLY|os.O_CREATE, 0o666)
		if err != nil {
			return fid, err
		}

		firstx := make([]byte, blocksize)
		read, err := file.ReadAt(firstx, i)
		logger.Debug(fmt.Sprintf("Bytes read: %d", read))

		if err != nil && err != io.EOF {
			return fid, err
		}
		firstx = firstx[:read]
		_, writeerr := f.Write(firstx)
		if writeerr != nil {
			return fid, err
		}
		f.Close()
	}
	if closer != nil {
		closer.Close()
	}
	return fid, nil
}

func SaveToDatabase(fid string, strcid string, db *leveldb.DB, logger log.Logger) error {
	err := db.Put(MakeDowntimeKey(strcid), []byte(fmt.Sprintf("%d", 0)), nil)
	if err != nil {
		logger.Error("Downtime Database Error: %v", err)
		return err
	}
	derr := db.Put(MakeFileKey(strcid), []byte(fid), nil)
	if derr != nil {
		logger.Error("File Database Error: %v", derr)
		return err
	}

	logger.Info(fmt.Sprintf("%s %s", fid, "Added to database"))

	_, cerr := db.Get(MakeFileKey(strcid), nil)
	if cerr != nil {
		logger.Error("Hash Database Error: %s", cerr.Error())
		return err
	}

	return nil
}
