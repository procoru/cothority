package service

import (
	"testing"

	"github.com/dedis/kyber"
	"github.com/dedis/kyber/sign/schnorr"
	"github.com/dedis/kyber/util/key"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
	"github.com/stretchr/testify/require"

	"fmt"
	"time"
)

var serviceID onet.ServiceID

func init() {
	serviceID = onet.ServiceFactory.ServiceID(Name)
}

func TestMain(m *testing.M) {
	log.MainTest(m)
}

func TestServiceSave(t *testing.T) {
	local := onet.NewTCPTest(tSuite)
	defer local.CloseAll()
	servers := local.GenServers(1)
	service := local.GetServices(servers, serviceID)[0].(*Service)
	service.data.Pin = "1234"
	service.save()
	service.data.Pin = ""
	log.ErrFatal(service.tryLoad())
	require.Equal(t, "1234", service.data.Pin)
}
func TestService_PinRequest(t *testing.T) {
	local := onet.NewTCPTest(tSuite)
	defer local.CloseAll()
	servers := local.GenServers(1)
	service := local.GetServices(servers, serviceID)[0].(*Service)
	require.Equal(t, "", service.data.Pin)
	pub := tSuite.Point().Pick(tSuite.XOF([]byte("test")))
	_, cerr := service.PinRequest(&PinRequest{"", pub})
	require.NotNil(t, cerr)
	require.NotEqual(t, "", service.data.Pin)
	_, cerr = service.PinRequest(&PinRequest{service.data.Pin, pub})
	log.Error(cerr)
	require.Equal(t, service.data.Public, pub)
}

func TestService_StoreConfig(t *testing.T) {
	local := onet.NewTCPTest(tSuite)
	defer local.CloseAll()
	nodes, r, _ := local.GenTree(2, true)
	service := local.GetServices(nodes, serviceID)[0].(*Service)
	desc := &PopDesc{
		Name:     "test",
		DateTime: "tomorrow",
		Roster:   onet.NewRoster(r.List),
	}
	kp := key.NewKeyPair(tSuite, rng)

	service.data.Public = kp.Public
	hash := desc.Hash()
	sg, err := schnorr.Sign(tSuite, rng, kp.Secret, hash)
	log.ErrFatal(err)
	msg, cerr := service.StoreConfig(&storeConfig{desc, sg})
	log.ErrFatal(cerr)
	_, ok := msg.(*storeConfigReply)
	require.True(t, ok)
	_, ok = service.data.Finals[string(desc.Hash())]
	require.True(t, ok)
}

func TestService_CheckConfigMessage(t *testing.T) {
	local := onet.NewTCPTest(tSuite)
	defer local.CloseAll()
	nodes, r, _ := local.GenTree(2, true)
	descs, atts, srvcs, _ := storeDesc(local.GetServices(nodes, serviceID), r, 2, 2)
	for _, s := range srvcs {
		for _, desc := range descs {
			hash := string(desc.Hash())
			s.data.Finals[hash].Attendees = make([]kyber.Point, len(atts))
			copy(s.data.Finals[hash].Attendees, atts)
		}
	}
	cc := &checkConfig{[]byte{}, atts}
	srvcs[0].SendRaw(r.List[1], cc)
	hash := string(descs[0].Hash())
	select {
	case <-srvcs[0].syncs[hash].ccChannel:
		require.Fail(t, "unexpected write on channel")
	case <-time.After(timeout / 60):
		break
	}
	cc.PopHash = []byte(hash)
	srvcs[0].SendRaw(r.List[1], cc)
	require.NotNil(t, <-srvcs[0].syncs[hash].ccChannel)
	require.Equal(t, 2, len(srvcs[0].data.Finals[hash].Attendees))
	require.Equal(t, 2, len(srvcs[1].data.Finals[hash].Attendees))

	cc.Attendees = atts[:1]
	srvcs[0].SendRaw(r.List[1], cc)
	require.NotNil(t, <-srvcs[0].syncs[hash].ccChannel)
	require.Equal(t, 1, len(srvcs[0].data.Finals[hash].Attendees))
	require.Equal(t, 1, len(srvcs[1].data.Finals[hash].Attendees))
}

func TestService_CheckConfigReply(t *testing.T) {
	local := onet.NewTCPTest(tSuite)
	defer local.CloseAll()
	nodes, r, _ := local.GenTree(2, true)
	descs, atts, srvcs, _ := storeDesc(local.GetServices(nodes, serviceID), r, 2, 2)
	for _, desc := range descs {
		hash := string(desc.Hash())
		s0 := srvcs[0]
		s0.data.Finals[hash].Attendees = make([]kyber.Point, len(atts))
		copy(s0.data.Finals[hash].Attendees, atts)

		ccr := &checkConfigReply{0, desc.Hash(), atts}
		req := &network.Envelope{
			Msg:            ccr,
			ServerIdentity: nodes[1].ServerIdentity,
		}

		s0.CheckConfigReply(req)
		<-s0.syncs[hash].ccChannel
		require.Equal(t, 2, len(s0.data.Finals[hash].Attendees))

		ccr.Attendees = atts[:1]
		req.Msg = ccr
		s0.CheckConfigReply(req)
		<-s0.syncs[hash].ccChannel
		require.Equal(t, 2, len(s0.data.Finals[hash].Attendees))

		ccr.PopStatus = PopStatusOK + 1
		req.Msg = ccr
		s0.CheckConfigReply(req)
		<-s0.syncs[hash].ccChannel
		require.Equal(t, 1, len(s0.data.Finals[hash].Attendees))
	}
}

func TestService_FinalizeRequest(t *testing.T) {
	local := onet.NewTCPTest(tSuite)
	defer local.CloseAll()
	nbrNodes := 3
	nbrAtt := 4
	ndescs := 2
	nodes, r, _ := local.GenTree(nbrNodes, true)

	// Get all service-instances
	descs, atts, services, privs := storeDesc(local.GetServices(nodes, serviceID), r, nbrAtt, ndescs)
	for _, desc := range descs {
		// Clear config of first one
		descHash := desc.Hash()
		delete(services[0].data.Finals, string(descHash))

		fr := &finalizeRequest{}
		fr.DescID = descHash
		fr.Attendees = atts
		hash, err := fr.hash()
		log.ErrFatal(err)
		// Send a request to all services
		for i, s := range services {
			sg, err := schnorr.Sign(tSuite, rng, privs[i], hash)
			log.ErrFatal(err)
			fr.Signature = sg
			_, err = s.FinalizeRequest(fr)
			require.NotNil(t, err)
		}

		sg, err := schnorr.Sign(tSuite, rng, privs[0], desc.Hash())
		log.ErrFatal(err)
		// Create a new config for the first one
		services[0].StoreConfig(&storeConfig{desc, sg})

		// Send a request to all services but the first one
		for i, s := range services {
			if i < 1 {
				continue
			}
			log.Lvl2("Asking", s, "to finalize")
			sg, err := schnorr.Sign(tSuite, rng, privs[i], hash)
			log.ErrFatal(err)
			fr.Signature = sg
			_, err = s.FinalizeRequest(fr)
			require.NotNil(t, err)
		}

		sg, err = schnorr.Sign(tSuite, rng, privs[0], hash)
		log.ErrFatal(err)
		fr.Signature = sg

		final, err := services[0].FinalizeRequest(fr)
		require.Nil(t, err)
		require.NotNil(t, final)
		fin, ok := final.(*finalizeResponse)
		require.True(t, ok)
		require.Nil(t, fin.Final.Verify())
	}
}

func TestService_FetchFinal(t *testing.T) {
	local := onet.NewTCPTest(tSuite)
	defer local.CloseAll()
	nbrNodes := 2
	nbrAtt := 1
	ndescs := 2
	nodes, r, _ := local.GenTree(nbrNodes, true)

	// Get all service-instances
	descs, atts, services, priv := storeDesc(local.GetServices(nodes, serviceID), r, nbrAtt, ndescs)
	for _, desc := range descs {
		descHash := desc.Hash()
		fr := &finalizeRequest{}
		fr.DescID = descHash
		fr.Attendees = atts
		hash, err := fr.hash()
		sg, err := schnorr.Sign(tSuite, rng, priv[0], hash)
		log.ErrFatal(err)
		fr.Signature = sg

		_, err = services[0].FinalizeRequest(fr)
		require.NotNil(t, err)

		sg, err = schnorr.Sign(tSuite, rng, priv[1], hash)
		log.ErrFatal(err)
		fr.Signature = sg

		msg, err := services[1].FinalizeRequest(fr)
		require.Nil(t, err)
		require.NotNil(t, msg)
		_, ok := msg.(*finalizeResponse)
		require.True(t, ok)
	}
	for _, desc := range descs {
		// Fetch final
		descHash := desc.Hash()
		for _, s := range services {
			msg, err := s.FetchFinal(&fetchRequest{descHash})
			require.Nil(t, err)
			require.NotNil(t, msg)
			resp, ok := msg.(*finalizeResponse)
			require.True(t, ok)
			final := resp.Final
			require.NotNil(t, final)
			require.Equal(t, final.Desc.Hash(), descHash)
			require.Nil(t, final.Verify())
		}
	}
}

func TestService_MergeConfig(t *testing.T) {
	local := onet.NewTCPTest(tSuite)
	defer local.CloseAll()
	nbrNodes := 4
	nbrAtt := 4
	nodes, r, _ := local.GenTree(nbrNodes, true)

	descs, atts, srvcs, priv := storeDescMerge(local.GetServices(nodes, serviceID), r, nbrAtt)
	hash := make([]string, nbrNodes/2)
	hash[0] = string(descs[0].Hash())
	hash[1] = string(descs[1].Hash())
	cc := &mergeConfig{srvcs[0].data.Finals[hash[0]], []byte{}}
	srvcs[0].SendRaw(r.List[1], cc)
	mcr := <-srvcs[0].syncs[hash[0]].mcChannel
	require.NotNil(t, mcr)
	require.Nil(t, mcr.Final)
	require.Equal(t, PopStatusWrongHash, mcr.PopStatus)

	require.Equal(t, nbrAtt, len(atts))

	cc.ID = []byte(hash[1])
	srvcs[0].SendRaw(r.List[2], cc)
	mcr = <-srvcs[0].syncs[hash[0]].mcChannel
	require.NotNil(t, mcr)
	require.Nil(t, mcr.Final)
	require.Equal(t, PopStatusMergeNonFinalized, mcr.PopStatus)
	// finish parties
	for i, desc := range descs {
		descHash := desc.Hash()

		fr := &finalizeRequest{}
		fr.DescID = descHash
		fr.Attendees = atts[2*i : 2*i+2]
		hash, err := fr.hash()
		sg, err := schnorr.Sign(tSuite, rng, priv[2*i], hash)
		log.ErrFatal(err)
		fr.Signature = sg
		_, err = srvcs[2*i].FinalizeRequest(fr)
		require.NotNil(t, err)

		sg, err = schnorr.Sign(tSuite, rng, priv[2*i+1], hash)
		log.ErrFatal(err)
		fr.Signature = sg
		msg, err := srvcs[2*i+1].FinalizeRequest(fr)
		require.Nil(t, err)
		require.NotNil(t, msg)
		_, ok := msg.(*finalizeResponse)
		require.True(t, ok)
	}

	log.Lvl2("Group 1, Server:", srvcs[0].ServerIdentity())
	log.Lvl2("Group 1, Server:", srvcs[1].ServerIdentity())
	log.Lvl2("Group 2, Server:", srvcs[2].ServerIdentity())
	log.Lvl2("Group 2, Server:", srvcs[3].ServerIdentity())
	cc.Final = srvcs[0].data.Finals[hash[0]]
	cc.ID = []byte(hash[1])
	srvcs[0].SendRaw(r.List[2], cc)
	_ = <-srvcs[0].syncs[hash[0]].mcChannel
	meta := srvcs[2].data.merges[hash[1]]
	require.Equal(t, len(meta.statementsMap), len(descs),
		"Server 2 statementsMap")
}

func TestService_MergeRequest(t *testing.T) {
	local := onet.NewTCPTest(tSuite)
	defer local.CloseAll()
	nbrNodes := 4
	nbrAtt := 4
	nodes, r, _ := local.GenTree(nbrNodes, true)
	descs, atts, srvcs, priv := storeDescMerge(local.GetServices(nodes, serviceID), r, nbrAtt)
	hash := make([]string, nbrNodes/2)
	hash[0] = string(descs[0].Hash())
	hash[1] = string(descs[1].Hash())

	// Wrong party check
	mr := &mergeRequest{}
	mr.ID = []byte(hash[1])
	sg, err := schnorr.Sign(tSuite, rng, priv[0], mr.ID)
	mr.Signature = sg
	log.ErrFatal(err)
	_, err = srvcs[0].MergeRequest(mr)
	require.NotNil(t, err)

	// Not finished
	mr.ID = []byte(hash[0])
	mr.Signature, err = schnorr.Sign(tSuite, rng, priv[0], mr.ID)
	log.ErrFatal(err)
	_, err = srvcs[0].MergeRequest(mr)
	require.NotNil(t, err)

	// finish parties
	for i, _ := range descs {

		fr := &finalizeRequest{}
		fr.DescID = []byte(hash[i])
		fr.Attendees = atts[2*i : 2*i+2]
		hash_fr, err := fr.hash()
		sg, err := schnorr.Sign(tSuite, rng, priv[2*i], hash_fr)
		log.ErrFatal(err)
		fr.Signature = sg
		_, err = srvcs[2*i].FinalizeRequest(fr)
		require.NotNil(t, err)

		sg, err = schnorr.Sign(tSuite, rng, priv[2*i+1], hash_fr)
		log.ErrFatal(err)
		fr.Signature = sg
		msg, err := srvcs[2*i+1].FinalizeRequest(fr)
		require.Nil(t, err)
		require.NotNil(t, msg)
		_, ok := msg.(*finalizeResponse)
		require.True(t, ok)
	}
	// wrong Signature
	mr.ID = []byte(hash[0])
	sg, err = schnorr.Sign(tSuite, rng, priv[1], mr.ID)
	log.ErrFatal(err)
	mr.Signature = sg
	_, err = srvcs[0].MergeRequest(mr)
	require.NotNil(t, err)
	//log.SetDebugVisible(2)
	log.Lvlf2("Group 1, Server: %s", srvcs[0].ServerIdentity())
	log.Lvlf2("Group 1, Server: %s", srvcs[1].ServerIdentity())
	log.Lvlf2("Group 2, Server: %s", srvcs[2].ServerIdentity())
	log.Lvlf2("Group 2, Server: %s", srvcs[3].ServerIdentity())
	mr.ID = []byte(hash[0])
	sg, err = schnorr.Sign(tSuite, rng, priv[0], mr.ID)
	log.ErrFatal(err)
	mr.Signature = sg
	msg, err := srvcs[0].MergeRequest(mr)
	require.Nil(t, err)
	require.NotNil(t, msg)
	for i, s := range srvcs {
		require.True(t, s.data.Finals[hash[i/2]].Merged,
			fmt.Sprintf("Server %d not Merged", i))
	}

	for i, s := range srvcs {
		require.Equal(t, len(s.data.Finals[hash[i/2]].Attendees),
			nbrAtt,
			fmt.Sprintf("Server %d attendees not merged", i))
		require.Equal(t,
			len(s.data.Finals[hash[i/2]].Desc.Roster.List),
			nbrNodes,
			fmt.Sprintf("Server %d conodes not merged", i))
		require.True(t, len(s.data.Finals[hash[i/2]].Signature) > 0 &&
			s.data.Finals[hash[i/2]].Verify() == nil,
			fmt.Sprintf("Signature in node %d is not created", i))
	}

}

func storeDesc(srvcs []onet.Service, el *onet.Roster, nbr int,
	nprts int) ([]*PopDesc, []kyber.Point, []*Service, []kyber.Scalar) {
	descs := make([]*PopDesc, nprts)
	for i := range descs {
		descs[i] = &PopDesc{
			Name:     "name",
			DateTime: "2017-07-31 00:00",
			Location: fmt.Sprintf("city%d", i),
			Roster:   onet.NewRoster(el.List),
		}
	}
	atts := make([]kyber.Point, nbr)
	for i := range atts {
		kp := key.NewKeyPair(tSuite, rng)
		atts[i] = kp.Public
	}

	pubs := make([]kyber.Point, len(srvcs))
	privs := make([]kyber.Scalar, len(srvcs))
	for i, _ := range srvcs {
		kp := key.NewKeyPair(tSuite, rng)
		pubs[i], privs[i] = kp.Public, kp.Secret
	}

	sret := []*Service{}
	for i, s := range srvcs {
		sret = append(sret, s.(*Service))
		s.(*Service).data.Public = pubs[i]
		for _, desc := range descs {
			hash := desc.Hash()
			sig, err := schnorr.Sign(tSuite, rng, privs[i], hash)
			log.ErrFatal(err)
			s.(*Service).StoreConfig(&storeConfig{desc, sig})
		}
	}
	return descs, atts, sret, privs
}

// Number of parties is assumed number of nodes / 2.
// Number of nodes is assumed to be even
func storeDescMerge(srvcs []onet.Service, el *onet.Roster, nbr int) ([]*PopDesc,
	[]kyber.Point, []*Service, []kyber.Scalar) {
	rosters := make([]*onet.Roster, len(el.List)/2)
	for i := 0; i < len(el.List); i += 2 {
		rosters[i/2] = onet.NewRoster(el.List[i : i+2])
	}
	descs := make([]*PopDesc, len(rosters))
	copy_descs := make([]*ShortDesc, len(rosters))
	for i := range descs {
		descs[i] = &PopDesc{
			Name:     "name",
			DateTime: "2017-07-31 00:00",
			Location: fmt.Sprintf("city%d", i),
			Roster:   rosters[i],
		}
		copy_descs[i] = &ShortDesc{
			Location: fmt.Sprintf("city%d", i),
			Roster:   rosters[i],
		}
	}
	for _, desc := range descs {
		desc.Parties = copy_descs
	}
	atts := make([]kyber.Point, nbr)

	for i := range atts {
		kp := key.NewKeyPair(tSuite, rng)
		atts[i] = kp.Public
	}

	pubs := make([]kyber.Point, len(srvcs))
	privs := make([]kyber.Scalar, len(srvcs))
	for i, _ := range srvcs {
		kp := key.NewKeyPair(tSuite, rng)
		pubs[i], privs[i] = kp.Public, kp.Secret
	}
	sret := []*Service{}
	for i, s := range srvcs {
		sret = append(sret, s.(*Service))
		s.(*Service).data.Public = pubs[i]
		desc := descs[i/2]
		hash := desc.Hash()
		sig, err := schnorr.Sign(tSuite, rng, privs[i], hash)
		log.ErrFatal(err)
		s.(*Service).StoreConfig(&storeConfig{desc, sig})
	}
	return descs, atts, sret, privs
}
