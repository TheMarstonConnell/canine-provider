package strays

import (
	"sync"

	"github.com/JackalLabs/jackal-provider/jprov/crypto"
	"github.com/JackalLabs/jackal-provider/jprov/utils"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/jackalLabs/canine-chain/x/storage/types"
	"github.com/spf13/cobra"
	"github.com/syndtr/goleveldb/leveldb"
)

type StrayQueue struct{}

type StrayManager struct {
	hands         []LittleHand
	Waiter        sync.WaitGroup
	Strays        []*types.Strays
	Context       *utils.Context
	ClientContext client.Context
	Address       string
	Cmd           *cobra.Command
	Ip            string
}

func NewStrayManager(cmd *cobra.Command) *StrayManager {
	clientCtx := client.GetClientContextFromCmd(cmd)
	ctx := utils.GetServerContextFromCmd(cmd)

	addr, err := crypto.GetAddress(clientCtx) // Getting the address of the provider to compare it to the strays.
	if err != nil {
		ctx.Logger.Error(err.Error())
		return nil
	}
	qClient := types.NewQueryClient(clientCtx)

	req := types.QueryProviderRequest{ // Ask the network what my own IP address is registered to.
		Address: addr,
	}

	provs, err := qClient.Providers(cmd.Context(), &req) // Publish the ask.
	if err != nil {
		ctx.Logger.Error(err.Error())
		return nil
	}
	ip := provs.Providers.Address // Our IP address

	return &StrayManager{
		hands:         []LittleHand{},
		Strays:        []*types.Strays{},
		Context:       ctx,
		Address:       addr,
		ClientContext: clientCtx,
		Cmd:           cmd,
		Ip:            ip,
	}
}

type LittleHand struct {
	Stray         *types.Strays
	Waiter        *sync.WaitGroup
	Database      *leveldb.DB
	Busy          bool
	Cmd           *cobra.Command
	ClientContext client.Context
}
