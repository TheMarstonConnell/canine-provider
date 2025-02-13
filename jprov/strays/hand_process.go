package strays

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/JackalLabs/jackal-provider/jprov/crypto"
	"github.com/JackalLabs/jackal-provider/jprov/utils"
	"github.com/cosmos/cosmos-sdk/client"
	txns "github.com/cosmos/cosmos-sdk/client/tx"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	storageTypes "github.com/jackalLabs/canine-chain/v3/x/storage/types"
	"github.com/spf13/pflag"
)

func (h *LittleHand) SearchFile(ctx *utils.Context, fid string) []string {
	qClient := storageTypes.NewQueryClient(h.ClientContext)
	fileRes, err := qClient.FindFile(h.Cmd.Context(), &storageTypes.QueryFindFileRequest{Fid: h.Stray.Fid}) // List all providers that currently have the file active.
	if err != nil {
		ctx.Logger.Error(err.Error())
		return nil // There was an issue, so we pretend like it didn't happen.
	}

	if fileRes == nil {
		return nil
	}

	var arr []string // Create an array of IPs from the request.
	err = json.Unmarshal([]byte(fileRes.ProviderIps), &arr)
	if err != nil {
		ctx.Logger.Error(err.Error())
		return nil // There was an issue, so we pretend like it didn't happen.
	}

	return arr
}

func (h *LittleHand) ClaimStray(m *StrayManager) error {
	msg := storageTypes.NewMsgClaimStray( // Attempt to claim the stray, this may fail if someone else has already tried to claim our stray.
		h.Address,
		h.Stray.Cid,
		m.Address,
	)
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	res, err := h.SendTx(h.ClientContext, h.Cmd.Flags(), msg)
	if err != nil {
		return err
	}

	if res == nil {
		return errors.New("nil transaction response")
	}

	if res.Code != 0 {
		return fmt.Errorf("unsuccessful transaction: %s", res.String())
	}

	return nil
}

func (h *LittleHand) Process(ctx *utils.Context, m *StrayManager) { // process the stray and make the txn, when done, free the hand & delete the stray entry
	m.Context.Logger.Info(fmt.Sprintf("Processing hand #%d", h.Id))
	if h.Stray == nil {
		m.Context.Logger.Info(fmt.Sprintf("Hand #%d is free.", h.Id))
		return
	}
	if h.Busy {
		m.Context.Logger.Info(fmt.Sprintf("Hand #%d is busy.", h.Id))
		return
	}
	h.Busy = true
	defer func() { // macro to free up hand
		m.Context.Logger.Info(fmt.Sprintf("Done processing hand #%d.", h.Id))
		h.Stray = nil
		h.Busy = false
	}()

	ctx.Logger.Info(fmt.Sprintf("Getting info for %s", h.Stray.Cid))
	arr := h.SearchFile(ctx, h.Stray.Fid)

	if len(arr) == 0 {
		/**
		If there are no providers with the file, we check if it's on our provider's filesystem. (We cannot claim
		strays that we don't own, but if we caused an error when handling the file we can reclaim the stray with
		the cached file from our filesystem which keeps the file alive)
		*/
		if h.Archive.FileExist(h.Stray.Fid) {
			ctx.Logger.Info(fmt.Sprintf("Nobody, not even I have %s.", h.Stray.Fid))
			return // If we don't have it and nobody else does, there is nothing we can do.
		}
	} else { // If there are providers with this file, we will download it from them instead to keep things consistent

		for _, prov := range arr {
			if prov == m.Ip { // Ignore ourselves
				return
			}
		}

		found := false
		for _, prov := range arr { // Check every provider for the file, not just trust chain data.
			if found {
				continue
			}

			err := h.DownloadFileFromURL(prov, h.Stray.Fid, h.Stray.Cid)
			if err != nil {
				ctx.Logger.Error(err.Error())
				continue
			}
			found = true // If we can successfully download the file, stop there.
		}

		if !found { // If we never find the file, and we don't have it, something is wrong with the network, nothing we can do.
			ctx.Logger.Info("Cannot find the file we want, either something is wrong or you have the file already")
			return
		}
	}

	ctx.Logger.Info(fmt.Sprintf("Attempting to claim %s on chain", h.Stray.Cid))

	err := h.ClaimStray(m)
	if err != nil {
		ctx.Logger.Error(fmt.Errorf("failed to claim stray: %w", err).Error())
		return
	}

	err = h.Database.SetContract(h.Stray.Cid, h.Stray.Fid)
	if err != nil {
		ctx.Logger.Error(err.Error())
		return
	}
}

func indexPrivKey(key string, index byte) (*cryptotypes.PrivKey, error) {
	keyData, err := hex.DecodeString(key)
	if err != nil {
		return nil, err
	}
	keyData[len(keyData)-1] += index
	k := cryptotypes.PrivKey{
		Key: keyData,
	}

	return &k, nil
}

func (h *LittleHand) prepareFactory(clientCtx client.Context, txf txns.Factory) (txns.Factory, error) {
	from, err := sdk.AccAddressFromBech32(h.Address)
	if err != nil {
		return txf, err
	}

	if err := txf.AccountRetriever().EnsureExists(clientCtx, from); err != nil {
		return txf, err
	}

	initNum, initSeq := txf.AccountNumber(), txf.Sequence()
	if initNum == 0 || initSeq == 0 {
		num, seq, err := txf.AccountRetriever().GetAccountNumberSequence(clientCtx, from)
		if err != nil {
			return txf, err
		}

		if initNum == 0 {
			txf = txf.WithAccountNumber(num)
		}

		if initSeq == 0 {
			txf = txf.WithSequence(seq)
		}
	}

	return txf, nil
}

func (h *LittleHand) SendTx(clientCtx client.Context, flagSet *pflag.FlagSet, msgs ...sdk.Msg) (*sdk.TxResponse, error) {
	txf := txns.NewFactoryCLI(clientCtx, flagSet)

	txf, err := h.prepareFactory(clientCtx, txf)
	if err != nil {
		return nil, err
	}

	if txf.SimulateAndExecute() || clientCtx.Simulate {
		_, adjusted, err := txns.CalculateGas(clientCtx, txf, msgs...)
		if err != nil {
			return nil, err
		}

		txf = txf.WithGas(adjusted)
		//_, _ = fmt.Fprintf(os.Stderr, "%s\n", txns.GasEstimateResponse{GasEstimate: txf.Gas()})
	}
	if clientCtx.Simulate {
		return nil, nil
	}

	tx, err := txns.BuildUnsignedTx(txf, msgs...)
	if err != nil {
		return nil, err
	}

	address, err := crypto.GetAddress(clientCtx)
	if err != nil {
		return nil, err
	}

	adr, err := sdk.AccAddressFromBech32(address)
	if err != nil {
		return nil, err
	}

	tx.SetFeeGranter(adr)
	err = h.Sign(txf, clientCtx, byte(h.Id), tx, true)
	if err != nil {
		return nil, err
	}

	txBytes, err := clientCtx.TxConfig.TxEncoder()(tx.GetTx())
	if err != nil {
		return nil, err
	}

	// broadcast to a Tendermint node
	res, err := clientCtx.BroadcastTx(txBytes)
	if err != nil {
		return nil, err
	}

	return res, err
}

func (h *LittleHand) Sign(txf txns.Factory, clientCtx client.Context, index byte, txBuilder client.TxBuilder, overwriteSig bool) error {
	signMode := txf.SignMode()
	if signMode == signing.SignMode_SIGN_MODE_UNSPECIFIED {
		// use the SignModeHandler's default mode if unspecified
		signMode = signing.SignMode_SIGN_MODE_DIRECT
	}

	pkeyStruct, err := crypto.ReadKey(clientCtx)
	if err != nil {
		return err
	}

	key, err := indexPrivKey(pkeyStruct.Key, index)
	if err != nil {
		return err
	}

	pubKey := key.PubKey()
	signerData := authsigning.SignerData{
		ChainID:       txf.ChainID(),
		AccountNumber: txf.AccountNumber(),
		Sequence:      txf.Sequence(),
	}

	// For SIGN_MODE_DIRECT, calling SetSignatures calls setSignerInfos on
	// TxBuilder under the hood, and SignerInfos is needed to generated the
	// sign bytes. This is the reason for setting SetSignatures here, with a
	// nil signature.
	//
	// Note: this line is not needed for SIGN_MODE_LEGACY_AMINO, but putting it
	// also doesn't affect its generated sign bytes, so for code's simplicity
	// sake, we put it here.
	sigData := signing.SingleSignatureData{
		SignMode:  signMode,
		Signature: nil,
	}
	sig := signing.SignatureV2{
		PubKey:   pubKey,
		Data:     &sigData,
		Sequence: txf.Sequence(),
	}
	var prevSignatures []signing.SignatureV2
	if !overwriteSig {
		prevSignatures, err = txBuilder.GetTx().GetSignaturesV2()
		if err != nil {
			return err
		}
	}
	if err := txBuilder.SetSignatures(sig); err != nil {
		return err
	}

	// Generate the bytes to be signed.
	bytesToSign, err := clientCtx.TxConfig.SignModeHandler().GetSignBytes(signMode, signerData, txBuilder.GetTx())
	if err != nil {
		return err
	}

	// Sign those bytes
	sigBytes, err := crypto.Sign(key, bytesToSign)
	if err != nil {
		return err
	}

	// Construct the SignatureV2 struct
	sigData = signing.SingleSignatureData{
		SignMode:  signMode,
		Signature: sigBytes,
	}
	sig = signing.SignatureV2{
		PubKey:   pubKey,
		Data:     &sigData,
		Sequence: txf.Sequence(),
	}

	if overwriteSig {
		return txBuilder.SetSignatures(sig)
	}
	prevSignatures = append(prevSignatures, sig)
	return txBuilder.SetSignatures(prevSignatures...)
}
