package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JackalLabs/jackal-provider/jprov/crypto"
	"github.com/JackalLabs/jackal-provider/jprov/queue"
	"github.com/JackalLabs/jackal-provider/jprov/types"
	"github.com/JackalLabs/jackal-provider/jprov/utils"
	"github.com/wealdtech/go-merkletree/sha3"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/syndtr/goleveldb/leveldb"

	"github.com/cosmos/cosmos-sdk/client"

	storageTypes "github.com/jackalLabs/canine-chain/x/storage/types"

	merkletree "github.com/wealdtech/go-merkletree"

	"github.com/spf13/cobra"
)

func GetMerkleTree(ctx client.Context, filename string) (*merkletree.MerkleTree, error) {
	rawTree, err := os.ReadFile(utils.GetStoragePathForTree(ctx.HomeDir, filename))
	if err != nil {
		return &merkletree.MerkleTree{}, fmt.Errorf("unable to find merkle tree for: %s", filename)
	}

	return merkletree.ImportMerkleTree(rawTree, sha3.New512())
}

func GenerateMerkleProof(tree merkletree.MerkleTree, index int, item []byte) (valid bool, proof *merkletree.Proof, err error) {
	h := sha256.New()
	_, err = io.WriteString(h, fmt.Sprintf("%d%x", index, item))
	if err != nil {
		return
	}

	proof, err = tree.GenerateProof(h.Sum(nil), 0)
	if err != nil {
		return
	}

	valid, err = merkletree.VerifyProofUsing(h.Sum(nil), false, proof, [][]byte{tree.Root()}, sha3.New512())
	return
}

func CreateMerkleForProof(clientCtx client.Context, filename string, blockSize, index int64, ctx *utils.Context) (string, string, error) {
	data, err := GetPiece(utils.GetContentsPath(clientCtx.HomeDir, filename), index, blockSize)
	if err != nil {
		return "", "", err
	}

	mTree, err := GetMerkleTree(clientCtx, filename)
	if err != nil {
		return "", "", err
	}

	verified, proof, err := GenerateMerkleProof(*mTree, int(index), data)
	if err != nil {
		ctx.Logger.Error(err.Error())
		return "", "", err
	}

	jproof, err := json.Marshal(*proof)
	if err != nil {
		return "", "", err
	}

	if !verified {
		ctx.Logger.Info("unable to generate valid proof")
	}

	return fmt.Sprintf("%x", data), string(jproof), nil
}

func requestAttestation(clientCtx client.Context, cid string, hashList string, item string, q *queue.UploadQueue) error {
	address, err := crypto.GetAddress(clientCtx)
	if err != nil {
		return err
	}

	msg := storageTypes.NewMsgRequestAttestationForm(
		address,
		cid,
	)
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(1)

	u := types.Upload{
		Message:  msg,
		Err:      nil,
		Callback: &wg,
		Response: nil,
	}

	q.Append(&u)
	wg.Wait()

	if u.Err != nil {
		fmt.Println(u.Err)
		return u.Err
	}

	if u.Response.Code != 0 {
		return fmt.Errorf(u.Response.RawLog)
	}

	var res storageTypes.MsgRequestAttestationFormResponse

	data, err := hex.DecodeString(u.Response.Data)
	if err != nil {
		fmt.Println(err)
		return err
	}

	var txMsgData sdk.TxMsgData

	err = clientCtx.Codec.Unmarshal(data, &txMsgData)
	if err != nil {
		fmt.Println(err)
		return err
	}

	for _, data := range txMsgData.Data {
		if data.GetMsgType() == "/canine_chain.storage.MsgRequestAttestationForm" {
			err := res.Unmarshal(data.Data)
			if err != nil {
				fmt.Println(err)
				return err
			}
			if res.Cid == cid {
				break
			}
		}
	}

	_ = clientCtx.PrintProto(&res)

	if !res.Success {
		fmt.Println("request form failed")
		fmt.Println(res.Error)
		return fmt.Errorf("failed to get attestations")
	}

	providerList := res.Providers
	var pwg sync.WaitGroup

	count := 0 // keep track of how many successful requests we've made

	for _, provider := range providerList { // request attestation from all providers, and wait until they all respond
		pwg.Add(1)

		prov := provider
		go func() {
			defer pwg.Done() // notify group that I have completed at the end of this function lifetime

			queryClient := storageTypes.NewQueryClient(clientCtx)

			provReq := &storageTypes.QueryProviderRequest{
				Address: prov,
			}

			providerDetails, err := queryClient.Providers(context.Background(), provReq)
			if err != nil {
				return
			}

			p := providerDetails.Providers
			providerAddress := p.Ip // get the providers IP address from chain at runtime

			path, err := url.JoinPath(providerAddress, "attest")
			if err != nil {
				return
			}

			attestRequest := types.AttestRequest{
				Cid:      cid,
				HashList: hashList,
				Item:     item,
			}

			data, err := json.Marshal(attestRequest)
			if err != nil {
				return
			}

			buf := bytes.NewBuffer(data)

			res, err := http.Post(path, "application/json", buf)
			if err != nil {
				return
			}

			if res.StatusCode == 200 {
				count += 1
			}
		}()

	}

	pwg.Wait()

	if count < 3 { // NOTE: this value can change in chain params
		fmt.Println("failed to get enough attestations...")
		return fmt.Errorf("failed to get attestations")
	}

	return nil
}

func postProof(clientCtx client.Context, cid string, blockSize, block int64, db *leveldb.DB, q *queue.UploadQueue, ctx *utils.Context) error {
	data, err := db.Get(utils.MakeFileKey(cid), nil)
	if err != nil {
		return err
	}

	item, hashlist, err := CreateMerkleForProof(clientCtx, string(data), blockSize, block, ctx)
	if err != nil {
		return err
	}

	address, err := crypto.GetAddress(clientCtx)
	if err != nil {
		ctx.Logger.Error(err.Error())
		return err
	}

	fmt.Printf("Requesting attestion for: %s\n", cid)

	err = requestAttestation(clientCtx, cid, hashlist, item, q) // request attestation, if we get it, skip all the posting
	if err == nil {
		fmt.Println("successfully got attestation.")
		return nil
	}

	msg := storageTypes.NewMsgPostproof(
		address,
		item,
		hashlist,
		cid,
	)
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(1)

	u := types.Upload{
		Message:  msg,
		Err:      nil,
		Callback: &wg,
		Response: nil,
	}

	go func() {
		q.Append(&u)
		wg.Wait()

		if u.Err != nil {

			ctx.Logger.Error(fmt.Sprintf("Posting Error: %s", u.Err.Error()))
			return
		}

		if u.Response.Code != 0 {
			ctx.Logger.Error("Contract Response Error: %s", fmt.Errorf(u.Response.RawLog))
			return
		}
	}()

	return nil
}

func postProofs(cmd *cobra.Command, db *leveldb.DB, q *queue.UploadQueue, ctx *utils.Context) {
	intervalFromCMD, err := cmd.Flags().GetUint16(types.FlagInterval)
	if err != nil {
		ctx.Logger.Error(err.Error())
		return
	}

	blockSize, err := cmd.Flags().GetInt64(types.FlagChunkSize)
	if err != nil {
		ctx.Logger.Error(err.Error())
		return
	}

	clientCtx, err := client.GetClientTxContext(cmd)
	if err != nil {
		ctx.Logger.Error(err.Error())
		return
	}

	maxMisses, err := cmd.Flags().GetInt(types.FlagMaxMisses)
	if err != nil {
		ctx.Logger.Error(err.Error())
		return
	}

	address, err := crypto.GetAddress(clientCtx)
	if err != nil {
		fmt.Println(err)
		return
	}

	for {
		interval := intervalFromCMD

		if interval == 0 { // If the provider picked an interval that's less than 30 minutes, we generate a random interval for them anyways

			r := rand.New(rand.NewSource(time.Now().UnixNano()))
			interval = uint16(r.Intn(3601) + 60) // Generate interval between 1-60 minutes

		}
		ctx.Logger.Debug(fmt.Sprintf("The interval between proofs is now %d", interval))
		start := time.Now()

		iter := db.NewIterator(nil, nil)

		for iter.Next() {
			cid := string(iter.Key())
			value := string(iter.Value())

			if cid[:len(utils.FileKey)] != utils.FileKey {
				continue
			}

			cid = cid[len(utils.FileKey):]

			ctx.Logger.Debug(fmt.Sprintf("filename: %s", value))

			ctx.Logger.Debug(fmt.Sprintf("CID: %s", cid))

			ver, verr := checkVerified(&clientCtx, cid, address)
			if verr != nil {
				ctx.Logger.Error(verr.Error())
				rr := strings.Contains(verr.Error(), "key not found")
				ny := strings.Contains(verr.Error(), ErrNotYours)
				if !rr && !ny {
					continue
				}
				val, err := db.Get(utils.MakeDowntimeKey(cid), nil)
				newval := 0
				if err == nil {
					newval, err = strconv.Atoi(string(val))
					if err != nil {
						continue
					}
				}

				newval += 1

				if newval > maxMisses {

					duplicate := false
					iter := db.NewIterator(nil, nil)
					for iter.Next() {
						c := string(iter.Key())
						v := string(iter.Value())

						if c[:len(utils.FileKey)] != utils.FileKey {
							continue
						}

						c = c[len(utils.FileKey):]

						if c != cid && v == value {
							ctx.Logger.Info(fmt.Sprintf("%s != %s but it is also %s, so we must keep the file on disk.", c, cid, v))
							duplicate = true
							break
						}
					}
					ctx.Logger.Info(fmt.Sprintf("%s is being removed", cid))

					if !duplicate {
						ctx.Logger.Info("And we are removing the file on disk.")

						err := os.RemoveAll(utils.GetFidDir(clientCtx.HomeDir, value))
						if err != nil {
							ctx.Logger.Error(err.Error())
						}

						err = os.Remove(utils.GetStoragePathForTree(clientCtx.HomeDir, value))
						if err != nil {
							ctx.Logger.Error(err.Error())
							continue
						}
					}
					err = db.Delete(utils.MakeFileKey(cid), nil)
					if err != nil {
						ctx.Logger.Error(err.Error())
						continue
					}

					err = db.Delete(utils.MakeDowntimeKey(cid), nil)
					if err != nil {
						ctx.Logger.Error(err.Error())
						continue
					}
					continue
				}

				ctx.Logger.Info(fmt.Sprintf("%s will be removed in %d cycles", value, (maxMisses+1)-newval))

				err = db.Put(utils.MakeDowntimeKey(cid), []byte(fmt.Sprintf("%d", newval)), nil)
				if err != nil {
					continue
				}
				continue
			}

			val, err := db.Get(utils.MakeDowntimeKey(cid), nil)
			newval := 0
			if err == nil {
				newval, err = strconv.Atoi(string(val))
				if err != nil {
					continue
				}
			}

			newval -= 1 // lower the downtime counter to only account for consecutive misses.
			if newval < 0 {
				newval = 0
			}

			err = db.Put(utils.MakeDowntimeKey(cid), []byte(fmt.Sprintf("%d", newval)), nil)
			if err != nil {
				continue
			}

			if ver {
				ctx.Logger.Debug("Skipping file as it's already verified.")
				continue
			}

			block, berr := queryBlock(&clientCtx, cid)
			if berr != nil {
				ctx.Logger.Error(fmt.Sprintf("Query Error: %v", berr))
				continue
			}

			dex, ok := sdk.NewIntFromString(block)
			ctx.Logger.Debug(fmt.Sprintf("BlockToProve: %s", block))
			if !ok {
				ctx.Logger.Error("cannot parse block number")
				continue
			}

			err = postProof(clientCtx, cid, blockSize, dex.Int64(), db, q, ctx)
			if err != nil {
				ctx.Logger.Error(fmt.Sprintf("Posting Proof Error: %v", err))
				continue
			}
			sleep, err := cmd.Flags().GetInt64(types.FlagSleep)
			if err != nil {
				ctx.Logger.Error(err.Error())
				continue
			}
			time.Sleep(time.Duration(sleep) * time.Millisecond)

		}

		iter.Release()
		err = iter.Error()
		if err != nil {
			ctx.Logger.Error("Iterator Error: %s", err.Error())
		}

		end := time.Since(start)
		if end.Seconds() > 120 {
			ctx.Logger.Error(fmt.Sprintf("proof took %d", end.Nanoseconds()))
		}

		tm := time.Duration(interval) * time.Second

		if tm.Nanoseconds()-end.Nanoseconds() > 0 {
			time.Sleep(time.Duration(interval) * time.Second)
		}

	}
}
