package skipchain

import (
	"errors"

	"strconv"

	"time"

	"fmt"

	"sync"

	"github.com/dedis/cothority/bftcosi"
	"github.com/dedis/cothority/messaging"
	"github.com/satori/go.uuid"
	"gopkg.in/dedis/crypto.v0/abstract"
	"gopkg.in/dedis/crypto.v0/cosi"
	"gopkg.in/dedis/crypto.v0/random"
	"gopkg.in/dedis/onet.v1"
	"gopkg.in/dedis/onet.v1/crypto"
	"gopkg.in/dedis/onet.v1/log"
	"gopkg.in/dedis/onet.v1/network"
)

// ServiceName can be used to refer to the name of this service
const ServiceName = "Skipchain"
const bftNewBlock = "SkipchainBFTNew"
const bftFollowBlock = "SkipchainBFTFollow"

// AuthSkipchain defines multiple levels of authentication for new skipblocks.
// The default value of 0 corresponds to the current usage and is the least
// secure, meaning anybody can add new skipchains and skipblocks, as long as they
// pass the verification. All values are:
//  - 0 : don't verify the roster of the skipblocks, accept everything
//  - 1 : accept only skipchains that are being followed, and if at least one
//        of the conodes is in the roster of a followed skipchain
//  - 2 : only accept new blocks if all conodes are in one of the followed rosters.
//        This is only for new blocks where no other skipchain is stored.
var AuthSkipchain int

func init() {
	skipchainSID, _ = onet.RegisterNewService(ServiceName, newSkipchainService)
	network.RegisterMessages(&Storage{})
}

// Only used in tests
var skipchainSID onet.ServiceID

// Name used to store skipblocks
const skipblocksID = "skipblocks"

// Service handles adding new SkipBlocks
type Service struct {
	*onet.ServiceProcessor
	Storage            *Storage
	propagate          messaging.PropagationFunc
	verifiers          map[VerifierID]SkipBlockVerifier
	blockRequestsMutex sync.Mutex
	blockRequests      map[string]chan *SkipBlock
	lastSave           time.Time
	newBlocksMutex     sync.Mutex
	newBlocks          map[string]bool
}

// Storage is saved to disk.
type Storage struct {
	// Sbm is the skipblock-map that holds all known skipblocks to this service.
	Sbm *SkipBlockMap
	// Follow is a slice of latest blocks that point to skipchains that are allowed
	// to create new blocks
	Follow []*SkipBlock
	// FollowIDs is a slice of IDs that have not been identified yet. Everytime
	// somebody asks this node to participate in a skipchain, and he's not in one
	// of the available skipblocks, he'll be asked if he knows that skipblock.
	FollowIDs []SkipBlockID
	// Clients is a list of public keys of clients that have successfully linked
	// to this service
	Clients []abstract.Point
}

// StoreSkipBlock stores a new skipblock in the system. This can be either a
// genesis-skipblock, that will create a new skipchain, or a new skipblock,
// that will be added to an existing chain.
//
// The conode servicing the request needs to be part of the actual valid latest
// skipblock, else it will fail.
//
// It takes a hash for the latest valid SkipBlock and a SkipBlock
// that will be verified. If the verification returns true, the new SkipBlock
// will be stored, else it will be discarded and an error will be returned.
//
// If the latest block given is nil it will create a new skipchain and store
// the given block as genesis-block.
//
// If the latest block is non-nil and exists, the skipblock will be added to the
// skipchain after verification that it fits and no other block already has been
// added.
func (s *Service) StoreSkipBlock(psbd *StoreSkipBlock) (*StoreSkipBlockReply, onet.ClientError) {
	prop := psbd.NewBlock
	if !s.ServerIdentity().Equal(prop.Roster.Get(0)) {
		return nil, onet.NewClientErrorCode(ErrorParameterWrong,
			"only leader is allowed to add blocks")
	}
	if AuthSkipchain > 0 {
		if psbd.Signature == nil {
			return nil, onet.NewClientErrorCode(ErrorParameterWrong,
				"cannot create new skipblock without authentication")
		}
		if !s.authenticate(psbd.NewBlock.CalculateHash(), *psbd.Signature) {
			return nil, onet.NewClientErrorCode(ErrorParameterWrong,
				"wrong signature for this skipchain")
		}
	}
	log.Lvlf2("Adding block to %x", psbd.LatestID)

	var prev *SkipBlock
	var changed []*SkipBlock

	if psbd.LatestID.IsNull() {
		// A new chain is created
		prop.Index = 0
		prop.Height = prop.MaximumHeight
		prop.ForwardLink = make([]*BlockLink, 0)
		// genesis block has a random back-link:
		bl := random.Bytes(32, random.Stream)
		prop.BackLinkIDs = []SkipBlockID{SkipBlockID(bl)}
		prop.GenesisID = nil
		prop.updateHash()
		err := s.verifyBlock(prop)
		if err != nil {
			return nil, onet.NewClientErrorCode(ErrorParameterWrong,
				err.Error())
		}
		if !s.newBlockStart(prop) {
			return nil, onet.NewClientErrorCode(ErrorBlockInProgress,
				"this skipchain-id is currently processing a block")
		}
		defer s.newBlockEnd(prop)

		if !prop.ParentBlockID.IsNull() {
			parent := s.Storage.Sbm.GetByID(prop.ParentBlockID)
			if parent == nil {
				return nil, onet.NewClientErrorCode(ErrorParameterWrong,
					"Didn't find parent")
			}
			parent.ChildSL = append(parent.ChildSL, prop.Hash)
			changed = append(changed, parent)
		}
		changed = append(changed, prop)

	} else {
		// We're appending a block to an existing chain
		prev = s.Storage.Sbm.GetByID(psbd.LatestID)
		if prev == nil {
			return nil, onet.NewClientErrorCode(ErrorBlockNotFound,
				"Didn't find latest block")
		}
		if i, _ := prev.Roster.Search(s.ServerIdentity().ID); i < 0 {
			return nil, onet.NewClientErrorCode(ErrorBlockContent,
				"We're not responsible for latest block")
		}
		if len(prev.ForwardLink) > 0 {
			return nil, onet.NewClientErrorCode(ErrorBlockContent,
				"the latest block already has a follower")
		}
		if !s.newBlockStart(prev) {
			return nil, onet.NewClientErrorCode(ErrorBlockInProgress,
				"this skipchain-id is currently processing a block")
		}
		defer s.newBlockEnd(prev)

		prop.MaximumHeight = prev.MaximumHeight
		prop.BaseHeight = prev.BaseHeight
		prop.ParentBlockID = nil
		prop.VerifierIDs = prev.VerifierIDs
		prop.Index = prev.Index + 1
		prop.GenesisID = prev.SkipChainID()
		index := prop.Index
		for prop.Height = 1; index%prop.BaseHeight == 0; prop.Height++ {
			index /= prop.BaseHeight
			if prop.Height >= prop.MaximumHeight {
				break
			}
		}
		log.Lvl4("Found height", prop.Height, "for index", prop.Index,
			"and maxHeight", prop.MaximumHeight, "and base", prop.BaseHeight)
		prop.BackLinkIDs = make([]SkipBlockID, prop.Height)
		pointer := prev
		for h := range prop.BackLinkIDs {
			for pointer.Height < h+1 {
				pointer = s.Storage.Sbm.GetByID(pointer.BackLinkIDs[0])
				if pointer == nil {
					return nil, onet.NewClientErrorCode(ErrorBlockNotFound,
						"Didn't find convenient SkipBlock for height "+
							strconv.Itoa(h))
				}
			}
			prop.BackLinkIDs[h] = pointer.Hash
		}
		prop.updateHash()

		if !prev.Roster.ID.Equal(prop.Roster.ID) {
			for _, si := range prop.Roster.List {
				if i, _ := prev.Roster.Search(si.ID); i == -1 {
					if !s.willNodeAcceptGenesis(si, prop.SkipChainID()) {
						return nil, onet.NewClientErrorCode(ErrorBlockContent,
							"node refused to accept new roster")
					}
				}
			}
		}

		if err := s.addForwardLink(prev, prop); err != nil {
			return nil, onet.NewClientErrorCode(ErrorBlockContent,
				"Couldn't get forward signature on block: "+err.Error())
		}
		changed = append(changed, prev, prop)
		for i, bl := range prop.BackLinkIDs[1:] {
			back := s.Storage.Sbm.GetByID(bl)
			if back == nil {
				return nil, onet.NewClientErrorCode(ErrorBlockContent,
					"Didn't get skipblock in back-link")
			}
			if err := s.forwardSignature(
				&ForwardSignature{i + 1, prev.Hash, prop,
					prev.GetForward(0)}); err != nil {
				// This is not a critical failure - we have at least
				// one forward-link
				log.Error("Couldn't get old block to sign: " + err.Error())
			} else {
				changed = append(changed, back)
			}
		}
	}
	if err := s.startPropagation(changed); err != nil {
		return nil, onet.NewClientErrorCode(ErrorVerification,
			"Couldn't propagate new blocks: "+err.Error())
	}
	s.save()
	reply := &StoreSkipBlockReply{
		Previous: prev,
		Latest:   prop,
	}
	return reply, nil
}

// GetUpdateChain returns a slice of SkipBlocks which describe the part of the
// skipchain from the latest block the caller knows of to the actual latest
// SkipBlock.
// Somehow comparable to search in SkipLists.
func (s *Service) GetUpdateChain(latestKnown *GetUpdateChain) (network.Message, onet.ClientError) {
	block := s.Storage.Sbm.GetByID(latestKnown.LatestID)
	if block == nil {
		return nil, onet.NewClientErrorCode(ErrorBlockNotFound, "Couldn't find latest skipblock")
	}
	// at least the latest know and the next block:
	blocks := []*SkipBlock{block}
	log.Lvlf3("Starting to search chain at %x", s.Context.ServerIdentity().ID[0:8])
	for block.GetForwardLen() > 0 {
		link := block.ForwardLink[block.GetForwardLen()-1]
		next := s.Storage.Sbm.GetByID(link.Hash)
		if next == nil {
			log.Lvl3("Didn't find next block, updating block")
			var err error
			next, err = s.callGetBlock(block, link.Hash)
			if err != nil {
				return nil, onet.NewClientErrorCode(ErrorBlockNotFound,
					err.Error())
			}
		} else {
			if i, _ := next.Roster.Search(s.ServerIdentity().ID); i < 0 {
				log.Lvl3("We're not responsible for", next, "- asking for update")
				var err error
				next, err = s.callGetBlock(next, link.Hash)
				if err != nil {
					return nil, onet.NewClientErrorCode(ErrorBlockNotFound,
						err.Error())
				}
			}
		}
		block = next
		blocks = append(blocks, next)
	}
	log.Lvl3("Found", len(blocks), "blocks")
	reply := &GetUpdateChainReply{blocks}

	return reply, nil
}

// GetSingleBlock searches for the given block and returns it. If no such block is
// found, a nil is returned.
func (s *Service) GetSingleBlock(id *GetSingleBlock) (*SkipBlock, onet.ClientError) {
	sb := s.Storage.Sbm.GetByID(id.ID)
	if sb == nil {
		return nil, onet.NewClientErrorCode(ErrorBlockNotFound,
			"No such block")
	}
	return sb, nil
}

// GetSingleBlockByIndex searches for the given block and returns it. If no such block is
// found, a nil is returned.
func (s *Service) GetSingleBlockByIndex(id *GetSingleBlockByIndex) (*SkipBlock, onet.ClientError) {
	sb := s.Storage.Sbm.GetByID(id.Genesis)
	if sb == nil {
		return nil, onet.NewClientErrorCode(ErrorBlockNotFound,
			"No such genesis-block")
	}
	if sb.Index == id.Index {
		return sb, nil
	}
	for len(sb.ForwardLink) > 0 {
		sb = s.Storage.Sbm.GetByID(sb.ForwardLink[0].Hash)
		if sb.Index == id.Index {
			return sb, nil
		}
	}
	return nil, onet.NewClientErrorCode(ErrorBlockNotFound,
		"No block with this index found")
}

// GetAllSkipchains returns a list of all known skipchains
func (s *Service) GetAllSkipchains(id *GetAllSkipchains) (*GetAllSkipchainsReply, onet.ClientError) {
	// Write all known skipblocks to a map, thus removing double blocks.
	chains := map[string]*SkipBlock{}
	s.Storage.Sbm.Lock()
	for _, sb := range s.Storage.Sbm.SkipBlocks {
		chains[string(sb.SkipChainID())] = sb
	}
	s.Storage.Sbm.Unlock()

	reply := &GetAllSkipchainsReply{
		SkipChains: make([]*SkipBlock, 0, len(chains)),
	}
	for _, sb := range chains {
		reply.SkipChains = append(reply.SkipChains, sb)
	}
	return reply, nil
}

// CreateLinkPrivate checks if the given public key is signed with our private
// key and stores it in the list of allowed clients if it is true.
func (s *Service) CreateLinkPrivate(link *CreateLinkPrivate) (*EmptyReply, onet.ClientError) {
	msg, err := link.Public.MarshalBinary()
	if err != nil {
		return nil, onet.NewClientErrorCode(ErrorOnet, "couldn't marshal public key: "+err.Error())
	}
	if err = crypto.VerifySchnorr(network.Suite, s.ServerIdentity().Public, msg, link.Signature); err != nil {
		return nil, onet.NewClientErrorCode(ErrorParameterWrong, "wrong signature on public key: "+err.Error())
	}
	s.Storage.Clients = append(s.Storage.Clients, link.Public)
	return &EmptyReply{}, nil
}

// SettingAuthentication stores whether this conode should verify the authentication
// of new skipblocks.
func (s *Service) SettingAuthentication(auth *SettingAuthentication) (*EmptyReply, onet.ClientError) {
	msg := []byte{byte(auth.Authentication)}
	if !s.verifySigs(msg, auth.Signature) {
		return nil, onet.NewClientErrorCode(ErrorParameterWrong, "wrong signature or unknown signer")
	}
	log.Print("got signature of", s.Storage.Clients)
	if auth.Authentication < 0 || auth.Authentication > 2 {
		return nil, onet.NewClientErrorCode(ErrorParameterWrong, "unknown Authentication option")
	}
	if auth.Authentication == 2 {
		return nil, onet.NewClientErrorCode(ErrorParameterWrong, "this authentication type is not yet supported")
	}
	AuthSkipchain = auth.Authentication
	return &EmptyReply{}, nil
}

// AddFollow adds a new skipchain to be followed
func (s *Service) AddFollow(add *AddFollow) (*EmptyReply, onet.ClientError) {
	msg := []byte{byte(add.SearchPolicy)}
	msg = append(add.SkipchainID, msg...)
	msg = append(msg, []byte(add.Conode)...)
	if !s.verifySigs(msg, add.Signature) {
		return nil, onet.NewClientErrorCode(ErrorParameterWrong, "wrong signature of unknown signer")
	}

	switch add.SearchPolicy {
	case 0:
		s.Storage.FollowIDs = append(s.Storage.FollowIDs, add.SkipchainID)
	case 1:
		// First search if anybody knows that SkipBlockID
		sis := map[string]*network.ServerIdentity{}
		for _, sb := range s.Storage.Follow {
			for _, si := range sb.Roster.List {
				sis[si.ID.String()] = si
			}
		}
		for sc := range s.Storage.Sbm.SkipBlocks {
			for _, si := range s.Storage.Sbm.GetByID(SkipBlockID(sc)).Roster.List {
				sis[si.ID.String()] = si
			}
		}
		found := false
		for _, si := range sis {
			roster := onet.NewRoster([]*network.ServerIdentity{si})
			reply, cerr := NewClient().GetUpdateChain(roster, add.SkipchainID)
			if cerr == nil {
				last := reply.Update[len(reply.Update)-1]
				if last.SkipChainID().Equal(add.SkipchainID) {
					s.Storage.Follow = append(s.Storage.Follow, last)
					found = true
					break
				}
			}
		}
		if !found {
			return nil, onet.NewClientErrorCode(ErrorParameterWrong, "didn't find that skipchain-id")
		}
	case 2:
		si := network.NewServerIdentity(network.Suite.Point(), network.NewTCPAddress(add.Conode))
		roster := onet.NewRoster([]*network.ServerIdentity{si})
		reply, cerr := NewClient().GetUpdateChain(roster, add.SkipchainID)
		if cerr != nil {
			return nil, onet.NewClientErrorCode(ErrorBlockNotFound, "didn't find skipchain at given address")
		}
		last := reply.Update[len(reply.Update)-1]
		if !last.SkipChainID().Equal(add.SkipchainID) {
			return nil, onet.NewClientErrorCode(ErrorBlockNotFound, "returned block is not correct")
		}
		s.Storage.Follow = append(s.Storage.Follow, last)
	default:
		return nil, onet.NewClientErrorCode(ErrorParameterWrong, "that SearchPolicy is not known.")
	}
	return &EmptyReply{}, nil
}

// IsPropagating returns true if there is at least one propagation running.
func (s *Service) IsPropagating() bool {
	s.newBlocksMutex.Lock()
	defer s.newBlocksMutex.Unlock()
	return len(s.newBlocks) > 0
}

// NewProtocol intercepts the creation of the skipblock protocol and
// initialises the necessary variables.
func (s *Service) NewProtocol(ti *onet.TreeNodeInstance, conf *onet.GenericConfig) (pi onet.ProtocolInstance, err error) {
	if ti.ProtocolName() == Name {
		pi, err = NewProtocol(ti)
		if err == nil {
			pisc := pi.(*Protocol)
			pisc.ERFollowers = &s.Storage.Follow
			pisc.ERFollowerIDs = s.Storage.FollowIDs
			pisc.GUSbm = s.Storage.Sbm
		}
	}
	return
}

func (s *Service) verifySigs(msg, sig []byte) bool {
	// If there are no clients, all signatures verify.
	if len(s.Storage.Clients) == 0 {
		return true
	}

	for _, cl := range s.Storage.Clients {
		if crypto.VerifySchnorr(network.Suite, cl, msg, sig) == nil {
			return true
		}
	}
	return false
}

func (s *Service) callGetBlock(known *SkipBlock, unknown SkipBlockID) (*SkipBlock, error) {
	s.blockRequestsMutex.Lock()
	request := make(chan *SkipBlock)
	s.blockRequests[string(unknown)] = request
	s.blockRequestsMutex.Unlock()
	defer func() {
		s.blockRequestsMutex.Lock()
		delete(s.blockRequests, string(unknown))
		s.blockRequestsMutex.Unlock()
	}()
	node := known.Roster.RandomServerIdentity()
	if err := s.SendRaw(node,
		&GetBlock{unknown}); err != nil {
		return nil, errors.New("Couldn't get updated block: " + known.Short())
	}
	var block *SkipBlock
	select {
	case block = <-request:
		log.Lvl3("Got block", block)
	case <-time.After(time.Millisecond * time.Duration(propagateTimeout)):
		return nil, errors.New("Couldn't get updated block in time: " + unknown.Short())
	}
	return block, nil
}

// forwardSignature receives a signature request of a newly accepted block.
// It only needs the 2nd-newest block and the forward-link.
func (s *Service) forwardSignature(fs *ForwardSignature) error {
	if fs.TargetHeight >= len(fs.Newest.BackLinkIDs) {
		return errors.New("This backlink-height doesn't exist")
	}
	target := s.Storage.Sbm.GetByID(fs.Newest.BackLinkIDs[fs.TargetHeight])
	if target == nil {
		return errors.New("Didn't find target-block")
	}
	data, err := network.Marshal(fs)
	if err != nil {
		return err
	}
	// TODO: is this really signed by target.roster?
	sig, err := s.startBFT(bftFollowBlock, target.Roster, fs.ForwardLink.Hash, data)
	if err != nil {
		return errors.New("Couldn't get signature: " + err.Error())
	}
	s.Storage.Sbm.Lock()
	log.Lvl1("Adding forward-link to", target.Index)
	target.AddForward(&BlockLink{fs.ForwardLink.Hash, sig.Sig})
	s.Storage.Sbm.Unlock()
	s.startPropagation([]*SkipBlock{target})
	return nil
}

func (s *Service) getBlock(env *network.Envelope) {
	gb, ok := env.Msg.(*GetBlock)
	if !ok {
		log.Error("Didn't receive GetBlock")
		return
	}
	sb := s.Storage.Sbm.GetByID(gb.ID)
	if sb == nil {
		log.Error("Did not find block")
		return
	}
	if i, _ := sb.Roster.Search(s.ServerIdentity().ID); i < 0 {
		log.Lvl3("Not responsible for that block, recursing")
		var err error
		sb, err = s.callGetBlock(sb, sb.Hash)
		if err != nil {
			log.Error(err)
			sb = nil
		}
	}
	if err := s.SendRaw(env.ServerIdentity, &GetBlockReply{sb}); err != nil {
		log.Error(err)
	}
}

func (s *Service) getBlockReply(env *network.Envelope) {
	gbr, ok := env.Msg.(*GetBlockReply)
	if !ok {
		log.Error("Didn't receive GetBlock")
		return
	}
	if err := s.Storage.Sbm.VerifyLinks(gbr.SkipBlock); err != nil {
		log.Error("Received invalid skipblock: " + err.Error())
	}
	id := s.Storage.Sbm.Store(gbr.SkipBlock)
	s.save()
	log.Lvl3("Sending block to channel")
	s.blockRequestsMutex.Lock()
	s.blockRequests[string(id)] <- gbr.SkipBlock
	s.blockRequestsMutex.Unlock()
}

// verifyFollowBlock makes sure that a signature-request for a forward-link
// is valid.
func (s *Service) bftVerifyFollowBlock(msg []byte, data []byte) bool {
	err := func() error {
		_, fsInt, err := network.Unmarshal(data)
		if err != nil {
			return err
		}
		fs, ok := fsInt.(*ForwardSignature)
		if !ok {
			return errors.New("Didn't receive a ForwardSignature")
		}
		previous := s.Storage.Sbm.GetByID(fs.Previous)
		if previous == nil {
			return errors.New("Didn't find newest block")
		}
		newest := fs.Newest
		if len(newest.BackLinkIDs) <= fs.TargetHeight {
			return errors.New("Asked for signing too high a backlink")
		}
		if err := fs.ForwardLink.VerifySignature(previous.Roster.Publics()); err != nil {
			return errors.New("Wrong forward-link signature: " + err.Error())
		}
		if !fs.ForwardLink.Hash.Equal(newest.Hash) {
			return errors.New("No forward-link from previous to newest")
		}
		target := s.Storage.Sbm.GetByID(newest.BackLinkIDs[fs.TargetHeight])
		s.Storage.Sbm.Lock()
		defer s.Storage.Sbm.Unlock()
		if target == nil {
			return errors.New("Don't have target-block")
		}
		if target.GetForwardLen() >= fs.TargetHeight+1 {
			return errors.New("Already have forward-link at height " +
				strconv.Itoa(fs.TargetHeight+1))
		}
		if !target.SkipChainID().Equal(newest.SkipChainID()) {
			return errors.New("Target and newest not from same skipchain")
		}
		return nil
	}()
	if err != nil {
		log.Error(err)
		return false
	}
	return true
}

// verifyNewBlock makes sure that a signature-request for a forward-link
// is valid.
func (s *Service) bftVerifyNewBlock(msg []byte, data []byte) bool {
	log.Lvlf4("%s verifying block %x", s.ServerIdentity(), msg)
	srcHash := data[0:32]
	prevSB := s.Storage.Sbm.GetByID(srcHash)
	if prevSB == nil {
		log.Error(s.ServerIdentity(), "Didn't find src-skipblock")
		return false
	}
	_, newSBi, err := network.Unmarshal(data[32:])
	if err != nil {
		log.Error("Couldn't unmarshal SkipBlock", data)
		return false
	}
	newSB := newSBi.(*SkipBlock)
	if !newSB.Hash.Equal(SkipBlockID(msg)) {
		log.Lvlf2("Dest skipBlock different from msg %x %x", msg, []byte(newSB.Hash))
		return false
	}

	if !newSB.BackLinkIDs[0].Equal(srcHash) {
		log.Lvl2("Backlink does not point to previous block:", prevSB.Index, newSB.Index)
		return false
	}
	if len(prevSB.ForwardLink) > 0 {
		log.Lvl2("previous block already has forward-link")
		return false
	}

	ok := func() bool {
		for _, ver := range newSB.VerifierIDs {
			f, ok := s.verifiers[ver]
			if !ok {
				log.Lvlf2("Found no user verification for %x", ver)
				return false
			}
			if !f(msg, newSB) {
				return false
			}
		}
		return true
	}()
	return ok
}

// PropagateSkipBlock will save a new SkipBlock
func (s *Service) propagateSkipBlock(msg network.Message) {
	sbs, ok := msg.(*PropagateSkipBlocks)
	if !ok {
		log.Error("Couldn't convert to slice of SkipBlocks")
		return
	}
	for _, sb := range sbs.SkipBlocks {
		if err := sb.VerifyForwardSignatures(); err != nil {
			log.Error(err)
			return
		}
		if !s.blockIsFriendly(sb) {
			log.Lvlf2("%s: block is not friendly: %x", s.ServerIdentity(), sb.Hash)
			return
		}
		s.Storage.Sbm.Store(sb)
		s.save()
	}
}

// RegisterVerification stores the verification in a map and will
// call it whenever a verification needs to be done.
func (s *Service) registerVerification(v VerifierID, f SkipBlockVerifier) error {
	s.verifiers[v] = f
	return nil
}

// checkBlock makes sure the basic parameters of a block are correct and returns
// an error if something fails.
func (s *Service) verifyBlock(sb *SkipBlock) error {
	if sb.MaximumHeight <= 0 {
		return errors.New("Set a maximumHeight > 0")
	}
	if sb.BaseHeight <= 0 {
		return errors.New("Set a baseHeight > 0")
	}
	if sb.MaximumHeight > sb.BaseHeight {
		return errors.New("maximumHeight must be smaller or equal baseHeight")
	}
	if sb.Index < 0 {
		return errors.New("Can't have an index < 0")
	}
	if len(sb.BackLinkIDs) <= 0 {
		return errors.New("Need at least one backlinkID")
	}
	if sb.Height < 1 {
		return errors.New("Minimum height is 1")
	}
	if sb.Height > sb.MaximumHeight {
		return errors.New("Height must be <= maximumHeight")
	}
	if sb.Roster == nil {
		return errors.New("Need a roster")
	}
	return nil
}

// addForwardLink verifies if the new block is valid. If it is not valid, it
// returns with an error.
// If it finds a valid block, a forward-link will be added and a BFT-signature
// requested.
func (s *Service) addForwardLink(src, dst *SkipBlock) error {
	if src.GetForwardLen() > 0 {
		return errors.New("already have forward-link at this height")
	}

	// create the message we want to sign for this round
	roster := src.Roster
	log.Lvlf3("%s is adding forward-link to %s: %d->%d", s.ServerIdentity(),
		roster.List, src.Index, dst.Index)
	data, err := network.Marshal(dst)
	if err != nil {
		return fmt.Errorf("Couldn't marshal block: %s", err.Error())
	}
	msg := []byte(dst.Hash)
	sig, err := s.startBFT(bftNewBlock, roster, msg, append(src.Hash, data...))
	if err != nil {
		return err
	}

	fwd := &BlockLink{
		Hash:      dst.Hash,
		Signature: sig.Sig,
	}
	fwl := s.Storage.Sbm.GetByID(src.Hash).ForwardLink
	log.Lvlf3("%s adds forward-link to %s: %d->%d - fwlinks:%v", s.ServerIdentity(),
		roster.List, src.Index, dst.Index, fwl)
	if len(fwl) > 0 {
		return errors.New("forward-link got signed during our signing")
	}
	src.ForwardLink = []*BlockLink{fwd}
	if err = src.VerifyForwardSignatures(); err != nil {
		return errors.New("Wrong BFT-signature: " + err.Error())
	}
	return nil
}

// startBFT starts a BFT-protocol with the given parameters.
func (s *Service) startBFT(proto string, roster *onet.Roster, msg, data []byte) (*bftcosi.BFTSignature, error) {
	tree := roster.GenerateNaryTreeWithRoot(2, s.ServerIdentity())
	if tree == nil {
		return nil, errors.New("couldn't form tree")
	}
	node, err := s.CreateProtocol(proto, tree)
	if err != nil {
		return nil, fmt.Errorf("couldn't create new node: %s", err.Error())
	}
	root := node.(*bftcosi.ProtocolBFTCoSi)

	switch len(roster.List) {
	case 0:
		return nil, errors.New("found empty Roster")
	case 1:
		pubs := []abstract.Point{s.ServerIdentity().Public}
		co := cosi.NewCosi(network.Suite, root.Private(), pubs)
		co.CreateCommitment(random.Stream)
		co.CreateChallenge(msg)
		co.CreateResponse()
		sig := &bftcosi.BFTSignature{
			Msg:        msg,
			Sig:        co.Signature(),
			Exceptions: []bftcosi.Exception{},
		}
		if cosi.VerifySignature(network.Suite, pubs, msg, sig.Sig) != nil {
			return nil, errors.New("failed in cosi")
		}
		return sig, nil
		//return nil, errors.New("need more than 1 entry for Roster")
	}

	// Start the protocol

	// Register the function generating the protocol instance
	root.Msg = msg
	root.Data = data

	// function that will be called when protocol is finished by the root
	done := make(chan bool)
	root.RegisterOnDone(func() {
		done <- true
	})
	go node.Start()
	var sig *bftcosi.BFTSignature
	select {
	case <-done:
		sig = root.Signature()
		if sig.Sig == nil {
			return nil, errors.New("couldn't sign forward-link")
		}
		return sig, nil
	case <-time.After(time.Second * 60):
		return nil, errors.New("timed out while waiting for signature")
	}
}

// notify other services about new/updated skipblock
func (s *Service) startPropagation(blocks []*SkipBlock) error {
	log.Lvl2("Starting to propagate for service", s.ServerIdentity())
	siMap := map[string]*network.ServerIdentity{}
	// Add all rosters of all blocks - everybody needs to be contacted
	for _, block := range blocks {
		for _, si := range block.Roster.List {
			siMap[uuid.UUID(si.ID).String()] = si
		}
	}
	siList := make([]*network.ServerIdentity, 0, len(siMap))
	for _, si := range siMap {
		siList = append(siList, si)
	}
	roster := onet.NewRoster(siList)

	replies, err := s.propagate(roster, &PropagateSkipBlocks{blocks}, propagateTimeout)
	if err != nil {
		return err
	}
	if replies != len(roster.List) {
		log.Warn("Did only get", replies, "out of", len(roster.List))
	}
	return nil
}

func (s *Service) newBlockStart(sb *SkipBlock) bool {
	s.newBlocksMutex.Lock()
	defer s.newBlocksMutex.Unlock()
	if _, processing := s.newBlocks[string(sb.Hash)]; processing {
		return false
	}
	if len(s.newBlocks) > 0 {
		return false
	}
	s.newBlocks[string(sb.Hash)] = true
	return true
}

func (s *Service) newBlockEnd(sb *SkipBlock) bool {
	s.newBlocksMutex.Lock()
	defer s.newBlocksMutex.Unlock()
	if _, processing := s.newBlocks[string(sb.Hash)]; !processing {
		return false
	}
	delete(s.newBlocks, string(sb.Hash))
	return true
}

// authenticate searches if this node or any follower-node can verify the
// schnorr-signature.
func (s *Service) authenticate(msg []byte, sig crypto.SchnorrSig) bool {
	if err := crypto.VerifySchnorr(network.Suite, s.ServerIdentity().Public, msg, sig); err == nil {
		return true
	}
	for _, sb := range s.Storage.Follow {
		for _, si := range sb.Roster.List {
			if err := crypto.VerifySchnorr(network.Suite, si.Public, msg, sig); err == nil {
				return true
			}
		}
	}
	for _, cl := range s.Storage.Clients {
		if err := crypto.VerifySchnorr(network.Suite, cl, msg, sig); err == nil {
			return true
		}
	}
	return false
}

// blockIsFriendly searches if all members of the new block are followed
// by this node.
func (s *Service) blockIsFriendly(sb *SkipBlock) bool {
	if AuthSkipchain == 0 {
		return true
	}
	// accept all blocks that are already stored with us.
	if s.Storage.Sbm.GetByID(sb.SkipChainID()) != nil {
		return true
	}
	// Also accept blocks that are stored in the FollowIDs
	for _, id := range s.Storage.FollowIDs {
		if id.Equal(sb.SkipChainID()) {
			return true
		}
	}
	switch AuthSkipchain {
	case 1:
		// Finally accept blocks that have parts of their roster
		// in one of our followers (except ourselves).
		for index, si1 := range sb.Roster.List {
			if !si1.Equal(s.ServerIdentity()) {
				for _, sb := range s.Storage.Follow {
					for _, si2 := range sb.Roster.List {
						if si1.Equal(si2) {
							return true
						}
					}
				}
			} else if len(sb.Roster.List) == 1 || index == 0 {
				return true
			}
		}
		return false
	default:
		log.Error("unknown AuthSkipchain type")
	}
	return true
}

// willNodeAcceptGenesis returns if the new node will accept to be in this roster.
func (s *Service) willNodeAcceptGenesis(si *network.ServerIdentity, genesis SkipBlockID) bool {
	roster := onet.NewRoster([]*network.ServerIdentity{s.ServerIdentity(), si})
	pi, err := s.CreateProtocol(Name, roster.GenerateBinaryTree())
	if err != nil {
		return false
	}
	pisc := pi.(*Protocol)
	pisc.ER = &ProtoExtendRoster{Genesis: genesis}
	pisc.Start()
	sigs := <-pisc.ERReply
	// TODO: store the sigs in the skipblock to prove the other node was OK
	return len(sigs) > 0
}

// saves all skipblocks.
func (s *Service) save() {
	s.Storage.Sbm.Lock()
	defer s.Storage.Sbm.Unlock()
	if time.Now().Sub(s.lastSave) < time.Second*timeBetweenSave {
		return
	}
	s.lastSave = time.Now()
	log.Lvl3("Saving service")
	err := s.Save(skipblocksID, s.Storage)
	if err != nil {
		log.Error("Couldn't save file:", err)
	}
}

// Tries to load the configuration and updates the data in the service
// if it finds a valid config-file.
func (s *Service) tryLoad() error {
	if !s.DataAvailable(skipblocksID) {
		return nil
	}
	msg, err := s.Load(skipblocksID)
	if err != nil {
		return err
	}
	var ok bool
	s.Storage, ok = msg.(*Storage)
	if !ok {
		return errors.New("data of wrong type")
	}
	return nil
}

func newSkipchainService(c *onet.Context) onet.Service {
	s := &Service{
		ServiceProcessor: onet.NewServiceProcessor(c),
		Storage:          &Storage{Sbm: NewSkipBlockMap()},
		verifiers:        map[VerifierID]SkipBlockVerifier{},
		blockRequests:    make(map[string]chan *SkipBlock),
		newBlocks:        make(map[string]bool),
	}
	if err := s.tryLoad(); err != nil {
		log.Error(err)
	}
	s.lastSave = time.Now()
	log.ErrFatal(s.RegisterHandlers(s.StoreSkipBlock, s.GetUpdateChain,
		s.GetSingleBlock, s.GetSingleBlockByIndex, s.GetAllSkipchains,
		s.CreateLinkPrivate, s.SettingAuthentication, s.AddFollow))
	s.ServiceProcessor.RegisterProcessorFunc(network.MessageType(GetBlock{}),
		s.getBlock)
	s.ServiceProcessor.RegisterProcessorFunc(network.MessageType(GetBlockReply{}),
		s.getBlockReply)

	log.ErrFatal(s.registerVerification(VerifyBase, s.verifyFuncBase))
	log.ErrFatal(s.registerVerification(VerifyRoot, s.verifyFuncRoot))
	log.ErrFatal(s.registerVerification(VerifyControl, s.verifyFuncControl))
	log.ErrFatal(s.registerVerification(VerifyData, s.verifyFuncData))

	var err error
	s.propagate, err = messaging.NewPropagationFunc(c, "SkipchainPropagate", s.propagateSkipBlock)
	log.ErrFatal(err)
	s.ProtocolRegister(bftNewBlock, func(n *onet.TreeNodeInstance) (onet.ProtocolInstance, error) {
		return bftcosi.NewBFTCoSiProtocol(n, s.bftVerifyNewBlock)
	})
	s.ProtocolRegister(bftFollowBlock, func(n *onet.TreeNodeInstance) (onet.ProtocolInstance, error) {
		return bftcosi.NewBFTCoSiProtocol(n, s.bftVerifyFollowBlock)
	})
	return s
}
