package chain

import (
	"context"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	cmtjson "github.com/cometbft/cometbft/libs/json"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/stretchr/testify/require"

	"github.com/babylonlabs-io/babylon/v3/test/e2e/containers"
	"github.com/babylonlabs-io/babylon/v3/test/e2e/initialization"
)

type NodeConfig struct {
	initialization.Node

	OperatorAddress  string
	SnapshotInterval uint64
	chainId          string
	rpcClient        *rpchttp.HTTP
	t                *testing.T
	containerManager *containers.Manager

	// Add this to help with logging / tracking time since start.
	setupTime time.Time
}

// NewNodeConfig returns new initialized NodeConfig.
func NewNodeConfig(t *testing.T, initNode *initialization.Node, initConfig *initialization.NodeConfig, chainId string, containerManager *containers.Manager) *NodeConfig {
	return &NodeConfig{
		Node:             *initNode,
		SnapshotInterval: initConfig.SnapshotInterval,
		chainId:          chainId,
		containerManager: containerManager,
		t:                t,
		setupTime:        time.Now(),
	}
}

// Run runs a node container for the given nodeIndex.
// The node configuration must be already added to the chain config prior to calling this
// method.
func (n *NodeConfig) Run() error {
	n.t.Logf("starting node container: %s", n.Name)
	resource, err := n.containerManager.RunNodeResource(n.chainId, n.Name, n.ConfigDir)
	if err != nil {
		return err
	}

	hostPort := resource.GetHostPort("26657/tcp")
	rpcClient, err := rpchttp.New("tcp://"+hostPort, "/websocket")
	if err != nil {
		return err
	}

	n.rpcClient = rpcClient

	require.Eventually(
		n.t,
		func() bool {
			// This fails if unsuccessful.
			_, err := n.QueryCurrentHeight()
			if err != nil {
				return false
			}
			n.t.Logf("started node container: %s", n.Name)
			return true
		},
		3*time.Minute,
		2*time.Second,
		"bbn node failed to produce blocks",
	)

	if err := n.extractOperatorAddressIfValidator(); err != nil {
		return err
	}

	return nil
}

// Stop stops the node from running and removes its container.
func (n *NodeConfig) Stop() error {
	n.t.Logf("stopping node container: %s", n.Name)
	if err := n.containerManager.RemoveNodeResource(n.Name); err != nil {
		return err
	}
	n.t.Logf("stopped node container: %s", n.Name)
	return nil
}

// WaitUntil waits until node reaches doneCondition. Return nil
// if reached, error otherwise.
func (n *NodeConfig) WaitUntil(doneCondition func(syncInfo coretypes.SyncInfo) bool) {
	var latestBlockHeight int64
	for i := 0; i < waitUntilrepeatMax; i++ {
		status, err := n.rpcClient.Status(context.Background())
		require.NoError(n.t, err)
		latestBlockHeight = status.SyncInfo.LatestBlockHeight
		// let the node produce a few blocks
		if !doneCondition(status.SyncInfo) {
			time.Sleep(waitUntilRepeatPauseTime)
			continue
		}
		return
	}
	n.t.Errorf("node %s timed out waiting for condition, latest block height was %d", n.Name, latestBlockHeight)
}

func (n *NodeConfig) LatestBlockNumber() uint64 {
	status, err := n.rpcClient.Status(context.Background())
	require.NoError(n.t, err)
	return uint64(status.SyncInfo.LatestBlockHeight)
}

func (n *NodeConfig) WaitForCondition(doneCondition func() bool, errorMsg string) {
	n.WaitForConditionWithPause(doneCondition, errorMsg, waitUntilRepeatPauseTime)
}

func (n *NodeConfig) WaitForConditionWithPause(doneCondition func() bool, errorMsg string, pause time.Duration) {
	for i := 0; i < waitUntilrepeatMax; i++ {
		if !doneCondition() {
			time.Sleep(pause)
			continue
		}
		return
	}
	n.t.Errorf("node %s timed out waiting for condition. Msg: %s", n.Name, errorMsg)
}

func (n *NodeConfig) WaitUntilBtcHeight(height uint32) {
	var latestBlockHeight uint32
	n.WaitForCondition(func() bool {
		btcTip, err := n.QueryTip()
		require.NoError(n.t, err)
		latestBlockHeight = btcTip.Height

		return latestBlockHeight >= height
	}, fmt.Sprintf("Timed out waiting for btc height %d", height))
}

func (n *NodeConfig) WaitForNextBlock() {
	n.WaitForNextBlocks(1)
}

func (n *NodeConfig) WaitForNextBlocks(numberOfBlocks uint64) {
	latest := n.LatestBlockNumber()
	blockToWait := latest + numberOfBlocks
	n.WaitForCondition(func() bool {
		newLatest := n.LatestBlockNumber()
		return newLatest > blockToWait
	}, fmt.Sprintf("Timed out waiting for block %d. Current height is: %d", latest, blockToWait))
}

func (n *NodeConfig) WaitForNextBlockWithSleep50ms() {
	latest := n.LatestBlockNumber()
	blockToWait := latest + 1
	n.WaitForConditionWithPause(func() bool {
		newLatest := n.LatestBlockNumber()
		return newLatest > blockToWait
	}, fmt.Sprintf("Timed out waiting for block %d. Current height is: %d", latest, blockToWait), time.Millisecond*50)
}

func (n *NodeConfig) extractOperatorAddressIfValidator() error {
	if !n.IsValidator {
		n.t.Logf("node (%s) is not a validator, skipping", n.Name)
		return nil
	}

	cmd := []string{"babylond", "debug", "addr", hex.EncodeToString(n.PublicKey)}
	n.t.Logf("extracting validator operator addresses for validator: %s", n.Name)
	_, errBuf, err := n.containerManager.ExecCmd(n.t, n.Name, cmd, "")
	if err != nil {
		return err
	}
	re := regexp.MustCompile("bbnvaloper(.{39})")
	operAddr := fmt.Sprintf("%s\n", re.FindString(errBuf.String()))
	n.OperatorAddress = strings.TrimSuffix(operAddr, "\n")
	return nil
}

func (n *NodeConfig) GetHostPort(portId string) (string, error) {
	return n.containerManager.GetHostPort(n.Name, portId)
}

func (n *NodeConfig) WithSetupTime(t time.Time) *NodeConfig {
	n.setupTime = t
	return n
}

func (n *NodeConfig) LogActionF(msg string, args ...interface{}) {
	timeSinceStart := time.Since(n.setupTime).Round(time.Millisecond)
	s := fmt.Sprintf(msg, args...)
	n.t.Logf("[%s] %s. From container %s", timeSinceStart, s, n.Name)
}

func (n *NodeConfig) Status() (*coretypes.ResultStatus, error) {
	cmd := []string{"babylond", "status", "--output=json"}
	outBuf, _, err := n.containerManager.ExecCmd(n.t, n.Name, cmd, "")
	if err != nil {
		return nil, err
	}

	var r coretypes.ResultStatus
	err = cmtjson.Unmarshal(outBuf.Bytes(), &r)
	if err != nil {
		return nil, err
	}

	return &r, nil
}

func (n *NodeConfig) ChainID() string {
	return n.chainId
}
