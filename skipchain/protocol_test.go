package skipchain_test

import (
	"testing"

	"github.com/dedis/cothority/skipchain"
	"github.com/stretchr/testify/require"
	"gopkg.in/dedis/onet.v1"
	"gopkg.in/dedis/onet.v1/crypto"
	"gopkg.in/dedis/onet.v1/log"
	"gopkg.in/dedis/onet.v1/network"
)

const tsName = "tsName"

// TestGU tests the GetUpdate message
func TestGU(t *testing.T) {
	tsid, err := onet.RegisterNewService(tsName, newTestService)
	log.ErrFatal(err)
	local := onet.NewLocalTest()
	defer local.CloseAll()
	servers, ro, _ := local.GenTree(2, true)
	tss := local.GetServices(servers, tsid)

	ts0 := tss[0].(*testService)
	ts1 := tss[1].(*testService)
	sb0 := skipchain.NewSkipBlock()
	sb0.Roster = ro
	sb0.Hash = sb0.CalculateHash()
	sb1 := skipchain.NewSkipBlock()
	sb1.BackLinkIDs = []skipchain.SkipBlockID{sb0.Hash}
	sb1.Hash = sb1.CalculateHash()
	bl := &skipchain.BlockLink{sb1.Hash, nil}
	sb0.ForwardLink = []*skipchain.BlockLink{bl}
	ts0.Sbm = skipchain.NewSkipBlockMap()
	ts0.Sbm.Store(sb0)
	ts0.Sbm.Store(sb1)
	sb := ts1.CallGU(sb0)
	require.Equal(t, sb1.Hash, sb.Hash)
}

// TestER tests the ProtoExtendRoster message
func TestER(t *testing.T) {
	tsid, err := onet.RegisterNewService(tsName, newTestService)
	log.ErrFatal(err)
	nodes := []int{2, 5, 13}
	for _, nbrNodes := range nodes {
		testER(t, tsid, nbrNodes)
	}
}

func testER(t *testing.T, tsid onet.ServiceID, nbrNodes int) {
	log.Lvl1("Testing", nbrNodes, "nodes")
	local := onet.NewLocalTest()
	defer local.CloseAll()
	genesis := []byte{1, 2, 3, 4}
	servers, roster, tree := local.GenBigTree(nbrNodes, nbrNodes, nbrNodes, true)
	tss := local.GetServices(servers, tsid)
	log.Lvl3(tree.Dump())

	ts := tss[0].(*testService)
	sigs := ts.CallER(tree, genesis)
	require.Equal(t, 0, len(sigs))

	sb := &skipchain.SkipBlock{SkipBlockFix: &skipchain.SkipBlockFix{Roster: roster}}
	for _, t := range tss {
		t.(*testService).Followers = []*skipchain.SkipBlock{sb}
	}

	sigs = ts.CallER(tree, genesis)
	require.Equal(t, nbrNodes-1, len(sigs))
	for _, s := range sigs {
		_, si := roster.Search(s.SI)
		require.NotNil(t, si)
		require.Nil(t, crypto.VerifySchnorr(network.Suite, si.Public, genesis, s.Signature))
	}

	if nbrNodes > 2 {
		for i := 2; i < nbrNodes; i++ {
			log.Lvl2("Checking failing signature at", i)
			tss[i].(*testService).Followers = nil
			sigs = ts.CallER(tree, genesis)
			require.Equal(t, 0, len(sigs))
			tss[i].(*testService).Followers = []*skipchain.SkipBlock{sb}
		}
	}
}

type testService struct {
	*onet.ServiceProcessor
	Followers []*skipchain.SkipBlock
	Sbm       *skipchain.SkipBlockMap
}

func (ts *testService) CallER(t *onet.Tree, g []byte) []skipchain.ProtoExtendSignature {
	pi, err := ts.CreateProtocol(skipchain.Name, t)
	if err != nil {
		return []skipchain.ProtoExtendSignature{}
	}
	pisc := pi.(*skipchain.Protocol)
	pisc.ER = &skipchain.ProtoExtendRoster{Genesis: g}
	if err := pi.Start(); err != nil {
		log.ErrFatal(err)
	}
	return <-pisc.ERReply
}

func (ts *testService) CallGU(sb *skipchain.SkipBlock) *skipchain.SkipBlock {
	t := onet.NewRoster([]*network.ServerIdentity{ts.ServerIdentity(), sb.Roster.List[0]}).GenerateBinaryTree()
	pi, err := ts.CreateProtocol(skipchain.Name, t)
	if err != nil {
		log.Error(err)
		return &skipchain.SkipBlock{}
	}
	pisc := pi.(*skipchain.Protocol)
	pisc.GU = &skipchain.ProtoGetUpdate{SBID: sb.Hash}
	if err := pi.Start(); err != nil {
		log.ErrFatal(err)
	}
	return <-pisc.GUReply
}

func (ts *testService) NewProtocol(ti *onet.TreeNodeInstance, conf *onet.GenericConfig) (pi onet.ProtocolInstance, err error) {
	pi, err = skipchain.NewProtocol(ti)
	if err == nil {
		pisc := pi.(*skipchain.Protocol)
		pisc.ERFollowers = &ts.Followers
		pisc.GUSbm = ts.Sbm
	}
	return
}

func newTestService(c *onet.Context) onet.Service {
	s := &testService{
		ServiceProcessor: onet.NewServiceProcessor(c),
	}
	return s
}
