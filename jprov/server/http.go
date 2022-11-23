package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/syndtr/goleveldb/leveldb"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/version"

	"github.com/julienschmidt/httprouter"

	api "github.com/JackalLabs/jackal-provider/jprov/api"
	"github.com/JackalLabs/jackal-provider/jprov/crypto"
	"github.com/JackalLabs/jackal-provider/jprov/queue"
	types "github.com/JackalLabs/jackal-provider/jprov/types"
	"github.com/JackalLabs/jackal-provider/jprov/utils"
	"github.com/spf13/cobra"
)

func indexres(cmd *cobra.Command, w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	clientCtx, err := client.GetClientTxContext(cmd)
	if err != nil {
		fmt.Println(err)
		return
	}

	address, err := crypto.GetAddress(clientCtx)
	if err != nil {
		fmt.Println(err)
		return
	}

	v := types.IndexResponse{
		Status:  "online",
		Address: address,
	}
	err = json.NewEncoder(w).Encode(v)
	if err != nil {
		fmt.Println(err)
	}
}

func checkVersion(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	v := types.VersionResponse{
		Version: version.Version,
	}
	err := json.NewEncoder(w).Encode(v)
	if err != nil {
		fmt.Println(err)
	}
}

func downfil(cmd *cobra.Command, w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	clientCtx := client.GetClientContextFromCmd(cmd)

	files, err := os.ReadDir(utils.GetStoragePath(clientCtx, ps.ByName("file")))
	if err != nil {
		fmt.Println(err)
		return
	}

	var data []byte

	for i := 0; i < len(files); i += 1 {
		f, err := os.ReadFile(filepath.Join(utils.GetStoragePath(clientCtx, ps.ByName("file")), fmt.Sprintf("%d.jkl", i)))
		if err != nil {
			fmt.Printf("Error can't open file!\n")
			_, err = w.Write([]byte("cannot find file"))
			if err != nil {
				fmt.Println(err)
			}
			return
		}

		data = append(data, f...)
	}

	_, err = w.Write(data)
	if err != nil {
		return
	}
}

func GetRoutes(cmd *cobra.Command, router *httprouter.Router, db *leveldb.DB, q *queue.UploadQueue) {
	dfil := func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		downfil(cmd, w, r, ps)
	}

	ires := func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		indexres(cmd, w, r, ps)
	}

	router.GET("/version", checkVersion)
	router.GET("/v", checkVersion)
	router.GET("/download/:file", dfil)
	router.GET("/d/:file", dfil)

	api.BuildApi(cmd, q, router, db)

	router.GET("/", ires)
}

func PostRoutes(cmd *cobra.Command, router *httprouter.Router, db *leveldb.DB, q *queue.UploadQueue) {
	upfil := func(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
		fileUpload(&w, r, ps, cmd, db, q)
	}

	router.POST("/upload", upfil)
	router.POST("/u", upfil)
}

// This function returns the filename(to save in database) of the saved file
// or an error if it occurs
func fileUpload(w *http.ResponseWriter, r *http.Request, ps httprouter.Params, cmd *cobra.Command, db *leveldb.DB, q *queue.UploadQueue) {
	// ParseMultipartForm parses a request body as multipart/form-data
	err := r.ParseMultipartForm(types.MaxFileSize) // MAX file size lives here
	if err != nil {
		fmt.Printf("Error with form file!\n")
		v := types.ErrorResponse{
			Error: err.Error(),
		}
		(*w).WriteHeader(http.StatusInternalServerError)
		err = json.NewEncoder(*w).Encode(v)
		if err != nil {
			fmt.Println(err)
		}
		return
	}
	sender := r.Form.Get("sender")

	file, handler, err := r.FormFile("file") // Retrieve the file from form data
	if err != nil {
		fmt.Printf("Error with form file!\n")
		v := types.ErrorResponse{
			Error: err.Error(),
		}
		(*w).WriteHeader(http.StatusInternalServerError)
		err = json.NewEncoder(*w).Encode(v)
		if err != nil {
			fmt.Println(err)
		}
		return
	}

	err = saveFile(file, handler, sender, cmd, db, w, q)
	if err != nil {
		v := types.ErrorResponse{
			Error: err.Error(),
		}
		(*w).WriteHeader(http.StatusInternalServerError)
		err = json.NewEncoder(*w).Encode(v)
		if err != nil {
			fmt.Println(err)
		}
	}
}
