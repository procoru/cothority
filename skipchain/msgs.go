package skipchain

import (
	"gopkg.in/dedis/crypto.v0/abstract"
	"gopkg.in/dedis/onet.v1"
	"gopkg.in/dedis/onet.v1/crypto"
	"gopkg.in/dedis/onet.v1/network"
)

func init() {
	network.RegisterMessages(
		// - API calls
		// Store new skipblock
		&StoreSkipBlock{},
		&StoreSkipBlockReply{},
		// Requests for data
		&GetUpdateChain{},
		&GetUpdateChainReply{},
		// Request updated block
		&GetSingleBlock{},
		// Fetch all skipchains
		&GetAllSkipchains{},
		&GetAllSkipchainsReply{},
		// Create link with client
		&CreateLinkPrivate{},
		// Setting authentication
		&SettingAuthentication{},
		// Adding a skipchain to follow
		&AddFollow{},
		// Removing a skipchain from following
		&DelFollow{},
		// EmptyReply for calls that only return errors
		&EmptyReply{},
		// Lists all skipchains we follow
		&ListFollow{},
		// Returns the genesis-blocks of all skipchains we follow
		&ListFollowReply{},
		// - Internal calls
		// Propagation
		&PropagateSkipBlocks{},
		// Request forward-signature
		&ForwardSignature{},
		// Request updated block
		&GetBlock{},
		// ERReply with updated block
		&GetBlockReply{},
		// - Data structures
		&SkipBlockFix{},
		&SkipBlock{},
		// Own service
		&Service{},
		// - Protocol messages
		&ProtoExtendSignature{},
		&ProtoExtendRosterReply{},
		&ProtoGetUpdate{},
		&ProtoBlockReply{},
	)
}

// This file holds all messages that can be sent to the SkipChain,
// both from the outside and between instances of this service

// External calls

// StoreSkipBlock - Requests a new skipblock to be appended to
// the given SkipBlock. If the given SkipBlock has Index 0 (which
// is invalid), a new SkipChain will be created.
// if AuthSkipchain == true, then the signature has to be a valid
// Schnorr signature on the hash of the NewBlock by either one of the
// conodes in the roster or by one of the clients.
type StoreSkipBlock struct {
	LatestID  SkipBlockID
	NewBlock  *SkipBlock
	Signature *crypto.SchnorrSig
}

// StoreSkipBlockReply - returns the signed SkipBlock with updated backlinks
type StoreSkipBlockReply struct {
	Previous *SkipBlock
	Latest   *SkipBlock
}

// GetUpdateChain - the client sends the hash of the last known
// Skipblock and will get back a list of all necessary SkipBlocks
// to get to the latest.
type GetUpdateChain struct {
	LatestID SkipBlockID
}

// GetUpdateChainReply - returns the shortest chain to the current SkipBlock,
// starting from the SkipBlock the client sent
type GetUpdateChainReply struct {
	Update []*SkipBlock
}

// GetAllSkipchains - returns all known last blocks of skipchains.
type GetAllSkipchains struct {
}

// GetAllSkipchainsReply - returns all known last blocks of skipchains.
type GetAllSkipchainsReply struct {
	SkipChains []*SkipBlock
}

// Internal calls

// PropagateSkipBlocks sends a newly signed SkipBlock to all members of
// the Cothority
type PropagateSkipBlocks struct {
	SkipBlocks []*SkipBlock
}

// ForwardSignature is called once a new skipblock has been accepted by
// signing the forward-link, and then the older skipblocks need to
// update their forward-links. Each cothority needs to get the necessary
// blocks and propagate the skipblocks itself.
type ForwardSignature struct {
	// TargetHeight is the index in the backlink-slice of the skipblock
	// to update
	TargetHeight int
	// Previous is the second-newest skipblock
	Previous SkipBlockID
	// Newest is the newest skipblock, signed by previous
	Newest *SkipBlock
	// ForwardLink is the signature from Previous to Newest
	ForwardLink *BlockLink
}

// GetSingleBlock asks for a single block.
type GetSingleBlock struct {
	ID SkipBlockID
}

// GetSingleBlockByIndex asks for a single block.
type GetSingleBlockByIndex struct {
	Genesis SkipBlockID
	Index   int
}

// Internal calls

// GetBlock asks for an updated block, in case for a conode that is not
// in the roster-list of that block.
type GetBlock struct {
	ID SkipBlockID
}

// PropagateSkipBlock sends a newly signed SkipBlock to all members of
// the Cothority
type PropagateSkipBlock struct {
	SkipBlock *SkipBlock
}

// GetBlockReply returns the requested block.
type GetBlockReply struct {
	SkipBlock *SkipBlock
}

// Protocol messages

// Name can be used to reference this protocol.
const Name = "sccomm"

// ProtoExtendSignature can be used as proof that a node accepted to be included
// in a new roster.
type ProtoExtendSignature struct {
	SI        network.ServerIdentityID
	Signature crypto.SchnorrSig
}

// ProtoExtendRoster asks a conode whether it would be OK to accept a new block
// with himself as part of the roster.
type ProtoExtendRoster struct {
	Genesis SkipBlockID
}

// ProtoStructExtendRoster embeds the treenode
type ProtoStructExtendRoster struct {
	*onet.TreeNode
	ProtoExtendRoster
}

// ProtoExtendRosterReply is a signature on the Genesis-id.
type ProtoExtendRosterReply struct {
	Signature *crypto.SchnorrSig
}

// ProtoStructExtendRosterReply embeds the treenode
type ProtoStructExtendRosterReply struct {
	*onet.TreeNode
	ProtoExtendRosterReply
}

// ProtoGetUpdate requests the latest block
type ProtoGetUpdate struct {
	SBID SkipBlockID
}

// ProtoStructGetUpdate embeds the treenode
type ProtoStructGetUpdate struct {
	*onet.TreeNode
	ProtoGetUpdate
}

// ProtoBlockReply returns a block - either from update or from getblock
type ProtoBlockReply struct {
	SkipBlock *SkipBlock
}

// ProtoStructBlockReply embeds the treenode
type ProtoStructBlockReply struct {
	*onet.TreeNode
	ProtoBlockReply
}

// CreateLinkPrivate asks to store the given public key in the list of administrative
// clients.
type CreateLinkPrivate struct {
	Public    abstract.Point
	Signature []byte
}

// EmptyReply is an empty reply. If there was an error in the
// request, it will be returned
type EmptyReply struct{}

// SettingAuthentication sets the authentication bit that enables restriction
// of the skipchains that are accepted. It needs to be signed by one of the
// clients. The signature is on []byte{0} if Authentication is false and on
// []byte{1} if the Authentication is true.
// TODO: perhaps we need to protect this against replay-attacks by adding a
// monotonically increasing nonce that is also stored on the conode.
type SettingAuthentication struct {
	Authentication bool
	Signature      []byte
}

// Adding a skipchain to follow. The Signature is on the SkipchainID.
type AddFollow struct {
	SkipchainID SkipBlockID
	Signature   []byte
}

// Removing a skipchain from following. The Signature is on the SkipchainID.
type DelFollow struct {
	SkipchainID SkipBlockID
	Signature   []byte
}

// Lists all skipchains we follow
type ListFollow struct {
}

// Returns the genesis-blocks of all skipchains we follow
type ListFollowReply struct {
	Follow []SkipBlock
}
