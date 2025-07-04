package cmd

import (
	"context"
	"fmt"
	"testing"

	dbm "github.com/cosmos/cosmos-db"

	"cosmossdk.io/log"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/server"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	genutiltest "github.com/cosmos/cosmos-sdk/x/genutil/client/testutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/babylonlabs-io/babylon/v3/app"
	"github.com/babylonlabs-io/babylon/v3/testutil/signer"
	checkpointingtypes "github.com/babylonlabs-io/babylon/v3/x/checkpointing/types"
)

func Test_TestnetCmd(t *testing.T) {
	home := t.TempDir()
	logger := log.NewNopLogger()
	cfg, err := genutiltest.CreateDefaultCometConfig(home)
	require.NoError(t, err)

	tbs, err := signer.SetupTestBlsSigner()
	require.NoError(t, err)
	blsSigner := checkpointingtypes.BlsSigner(tbs)
	appOpts, cleanup := app.TmpAppOptions()
	defer cleanup()

	bbn := app.NewBabylonAppWithCustomOptions(t, false, blsSigner, app.SetupOptions{
		Logger:             logger,
		DB:                 dbm.NewMemDB(),
		InvCheckPeriod:     0,
		SkipUpgradeHeights: map[int64]bool{},
		AppOpts:            appOpts,
	})
	err = genutiltest.ExecInitCmd(bbn.BasicModuleManager, home, bbn.AppCodec())
	require.NoError(t, err)

	serverCtx := server.NewContext(viper.New(), cfg, logger)
	clientCtx := client.Context{}.
		WithCodec(bbn.AppCodec()).
		WithInterfaceRegistry(bbn.InterfaceRegistry()).
		WithLegacyAmino(bbn.LegacyAmino()).
		WithTxConfig(bbn.TxConfig()).
		WithHomeDir(home)

	ctx := context.Background()
	ctx = context.WithValue(ctx, server.ServerContextKey, serverCtx)
	ctx = context.WithValue(ctx, client.ClientContextKey, &clientCtx)
	cmd := TestnetCmd(bbn.BasicModuleManager, banktypes.GenesisBalancesIterator{})
	cmd.SetArgs([]string{fmt.Sprintf("--%s=test", flags.FlagKeyringBackend), fmt.Sprintf("--output-dir=%s", home)})
	err = cmd.ExecuteContext(ctx)
	require.NoError(t, err)

	genFile := cfg.GenesisFile()
	appState, _, err := genutiltypes.GenesisStateFromGenFile(genFile)
	require.NoError(t, err)

	bankGenState := banktypes.GetGenesisStateFromAppState(bbn.AppCodec(), appState)
	require.NotEmpty(t, bankGenState.Supply.String())
}
