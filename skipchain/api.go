package skipchain

import (
	"gopkg.in/dedis/crypto.v0/abstract"
	"gopkg.in/dedis/onet.v1"
	"gopkg.in/dedis/onet.v1/crypto"
	"gopkg.in/dedis/onet.v1/log"
	"gopkg.in/dedis/onet.v1/network"
)

const (
	// ErrorBlockNotFound indicates that for any number of operations the
	// corresponding block has not been found.
	ErrorBlockNotFound = 4100 + iota
	// ErrorBlockNoParent indicates that a parent should be there but hasn't
	// been found.
	ErrorBlockNoParent
	// ErrorBlockContent indicates that part of a block is in an invalid state.
	ErrorBlockContent
	// ErrorParameterWrong indicates that a given parameter is out of bounds.
	ErrorParameterWrong
	// ErrorVerification indicates that a given block could not be verified
	// and a signature is invalid.
	ErrorVerification
	// ErrorOnet indicates an error from the onet framework
	ErrorOnet
	// ErrorBlockInProgress indicates that currently a block is being formed
	// and propagated
	ErrorBlockInProgress
)

// Client is a structure to communicate with the Skipchain
// service from the outside
type Client struct {
	*onet.Client
}

// NewClient instantiates a new client with name 'n'
func NewClient() *Client {
	return &Client{Client: onet.NewClient("Skipchain")}
}

// StoreSkipBlockSignature asks the cothority to store the new skipblock, and eventually
// attach it to the 'latest' skipblock.
//  - latest is the skipblock where the new skipblock is appended. If ro and d
//   are nil, a new skipchain will be created with 'latest' as genesis-block.
//  - ro is the new roster for that block. If ro is nil, the previous roster
//   will be used.
//  - d is the data for the new block. It can be nil. If it is not of type
//   []byte, it will be marshalled using `network.Marshal`.
//  - priv is the private key that will be used to sign the skipblock. If priv
//   is nil, the skipblock will not be signed.
func (c *Client) StoreSkipBlockSignature(latest *SkipBlock, ro *onet.Roster, d network.Message, priv abstract.Scalar) (reply *StoreSkipBlockReply, cerr onet.ClientError) {
	log.Lvlf3("%#v", latest)
	var newBlock *SkipBlock
	var latestID SkipBlockID
	if ro == nil && d == nil {
		newBlock = latest
	} else {
		newBlock = latest.Copy()
		if ro != nil {
			newBlock.Roster = ro
		}
		if d != nil {
			var ok bool
			newBlock.Data, ok = d.([]byte)
			if !ok {
				buf, err := network.Marshal(d)
				if err != nil {
					return nil, onet.NewClientErrorCode(ErrorParameterWrong,
						"Couldn't marshal data: "+err.Error())
				}
				newBlock.Data = buf
			}
		}
		latestID = latest.Hash
	}
	host := latest.Roster.Get(0)
	reply = &StoreSkipBlockReply{}
	var sig crypto.SchnorrSig
	if priv != nil {
		var err error
		sig, err = crypto.SignSchnorr(network.Suite, priv, newBlock.CalculateHash())
		if err != nil {
			return nil, onet.NewClientErrorCode(ErrorParameterWrong, "couldn't sign block: "+err.Error())
		}
	}
	cerr = c.SendProtobuf(host, &StoreSkipBlock{LatestID: latestID, NewBlock: newBlock,
		Signature: &sig}, reply)
	if cerr != nil {
		return nil, cerr
	}
	return reply, nil
}

// StoreSkipBlock asks the cothority to store the new skipblock, and eventually
// attach it to the 'latest' skipblock.
//  - latest is the skipblock where the new skipblock is appended. If ro and d
//   are nil, a new skipchain will be created with 'latest' as genesis-block.
//  - ro is the new roster for that block. If ro is nil, the previous roster
//   will be used.
//  - d is the data for the new block. It can be nil. If it is not of type
//   []byte, it will be marshalled using `network.Marshal`.
func (c *Client) StoreSkipBlock(latest *SkipBlock, ro *onet.Roster, d network.Message) (reply *StoreSkipBlockReply, cerr onet.ClientError) {
	return c.StoreSkipBlockSignature(latest, ro, d, nil)
}

// CreateGenesisSignature is a convenience function to create a new SkipChain with the
// given parameters.
//  - ro is the responsible roster
//  - baseH is the base-height - the distance between two non-height-1 skipblocks
//  - maxH is the maximum height, which must be <= baseH
//  - ver is a slice of verifications to apply to that block
//  - data can be nil or any data that will be network.Marshaled to the skipblock,
//    except if the data is of type []byte, in which case it will be stored
//    as-is on the skipchain.
//  - parent is the responsible parent-block, can be 'nil'
//  - priv is a private key that is allowed to sign for new skipblocks
//
// This function returns the created skipblock or nil and an error.
func (c *Client) CreateGenesisSignature(ro *onet.Roster, baseH, maxH int, ver []VerifierID,
	data interface{}, parent SkipBlockID, priv abstract.Scalar) (*SkipBlock, onet.ClientError) {
	genesis := NewSkipBlock()
	genesis.Roster = ro
	genesis.VerifierIDs = ver
	genesis.MaximumHeight = maxH
	genesis.BaseHeight = baseH
	genesis.ParentBlockID = parent
	if data != nil {
		var ok bool
		genesis.Data, ok = data.([]byte)
		if !ok {
			buf, err := network.Marshal(data)
			if err != nil {
				return nil, onet.NewClientErrorCode(ErrorParameterWrong,
					"Couldn't marshal data: "+err.Error())
			}
			genesis.Data = buf
		}
	}
	sb, cerr := c.StoreSkipBlockSignature(genesis, nil, nil, priv)
	if cerr != nil {
		return nil, cerr
	}
	return sb.Latest, nil
}

// CreateGenesis is a convenience function to create a new SkipChain with the
// given parameters.
//  - ro is the responsible roster
//  - baseH is the base-height - the distance between two non-height-1 skipblocks
//  - maxH is the maximum height, which must be <= baseH
//  - ver is a slice of verifications to apply to that block
//  - data can be nil or any data that will be network.Marshaled to the skipblock,
//    except if the data is of type []byte, in which case it will be stored
//    as-is on the skipchain.
//  - parent is the responsible parent-block, can be 'nil'
//
// This function returns the created skipblock or nil and an error.
func (c *Client) CreateGenesis(ro *onet.Roster, baseH, maxH int, ver []VerifierID,
	data interface{}, parent SkipBlockID) (*SkipBlock, onet.ClientError) {
	return c.CreateGenesisSignature(ro, baseH, maxH, ver, data, parent, nil)
}

// CreateRootControl is a convenience function and creates two Skipchains:
// a root SkipChain with maximumHeight of maxHRoot and a control SkipChain with
// maximumHeight of maxHControl. It connects both chains for later
// reference. The root-chain will use `VerificationRoot` and the config-chain
// will use `VerificationConfig`.
//
// A slice of verification-functions is given for the root and the control
// skipchain.
func (c *Client) CreateRootControl(elRoot, elControl *onet.Roster,
	keys []abstract.Point, baseHeight,
	maxHRoot, maxHControl int) (root, control *SkipBlock, cerr onet.ClientError) {
	log.Lvl2("Creating root roster", elRoot)
	root, cerr = c.CreateGenesis(elRoot, baseHeight, maxHRoot,
		VerificationRoot, nil, nil)
	if cerr != nil {
		return
	}
	log.Lvl2("Creating control roster", elControl)
	control, cerr = c.CreateGenesis(elControl, baseHeight, maxHControl,
		VerificationControl, nil, root.Hash)
	if cerr != nil {
		return
	}
	return root, control, cerr
}

// GetUpdateChain will return the chain of SkipBlocks going from the 'latest' to
// the most current SkipBlock of the chain. It takes a roster that knows the
// 'latest' skipblock and the id (=hash) of the latest skipblock.
func (c *Client) GetUpdateChain(roster *onet.Roster, latest SkipBlockID) (reply *GetUpdateChainReply, cerr onet.ClientError) {
	reply = &GetUpdateChainReply{}
	r := roster.RandomServerIdentity()
	cerr = c.SendProtobuf(r,
		&GetUpdateChain{latest}, reply)
	return
}

// GetAllSkipchains returns all skipchains known to that conode. If none are
// known, an empty slice is returned.
func (c *Client) GetAllSkipchains(si *network.ServerIdentity) (reply *GetAllSkipchainsReply,
	cerr onet.ClientError) {
	reply = &GetAllSkipchainsReply{}
	cerr = c.SendProtobuf(si, &GetAllSkipchains{}, reply)
	return
}

// GetSingleBlock searches for a block with the given ID and returns that block,
// or an error if that block is not found.
func (c *Client) GetSingleBlock(roster *onet.Roster, id SkipBlockID) (reply *SkipBlock, cerr onet.ClientError) {
	reply = &SkipBlock{}
	cerr = c.SendProtobuf(roster.RandomServerIdentity(),
		&GetSingleBlock{id}, reply)
	return
}

// GetSingleBlockByIndex searches for a block with the given index following the genesis-block.
// It returns that block, or an error if that block is not found.
func (c *Client) GetSingleBlockByIndex(roster *onet.Roster, genesis SkipBlockID, index int) (reply *SkipBlock, cerr onet.ClientError) {
	reply = &SkipBlock{}
	cerr = c.SendProtobuf(roster.RandomServerIdentity(),
		&GetSingleBlockByIndex{genesis, index}, reply)
	return
}

// CreateLinkPrivate asks the conode to create a link by sending a public
// key of the client, signed by the private key of the conode. The reasoning is
// that an administrator should well be able to copy the private.toml-file from
// the server and use that to authenticate and link to the conode.
func (c *Client) CreateLinkPrivate(si *network.ServerIdentity, conodePriv abstract.Scalar,
	pub abstract.Point) onet.ClientError {
	reply := &EmptyReply{}
	msg, err := pub.MarshalBinary()
	if err != nil {
		return onet.NewClientErrorCode(ErrorOnet, "couldn't marshal point: "+err.Error())
	}
	sig, err := crypto.SignSchnorr(network.Suite, conodePriv, msg)
	if err != nil {
		return onet.NewClientErrorCode(ErrorOnet, "couldn't sign public key: "+err.Error())
	}
	return c.SendProtobuf(si, &CreateLinkPrivate{Public: pub, Signature: sig}, reply)
}

// SettingAuthentication enables or disables the authentication handling of the
// skipchain. If it is set to true, the skipchain will only allow handling of
// skipchains where _all_ rosters are in the combined set of rosters stored in
// the latest blocks of all skipchains stored in the `Follow`-field of the
// skipchain-service.
func (c *Client) SettingAuthentication(si *network.ServerIdentity, clientPriv abstract.Scalar, auth int) onet.ClientError {
	sig, err := crypto.SignSchnorr(network.Suite, clientPriv, []byte{byte(auth)})
	if err != nil {
		return onet.NewClientErrorCode(ErrorParameterWrong, "couldn't create signature:"+err.Error())
	}
	req := &SettingAuthentication{
		Authentication: auth,
		Signature:      sig,
	}
	return c.SendProtobuf(si, req, nil)
}

// AddFollow gives a skipchain-id to the conode that should be used to allow/disallow
// new blocks. Only if SettingAuthentication(true) has been called is this active.
func (c *Client) AddFollow(si *network.ServerIdentity, clientPriv abstract.Scalar,
	scid SkipBlockID, searchPolicy int, conode string) onet.ClientError {
	req := &AddFollow{
		SkipchainID:  scid,
		SearchPolicy: searchPolicy,
		Conode:       conode,
	}
	msg := []byte{byte(searchPolicy)}
	msg = append(scid, msg...)
	msg = append(msg, []byte(conode)...)
	sig, err := crypto.SignSchnorr(network.Suite, clientPriv, msg)
	if err != nil {
		return onet.NewClientErrorCode(ErrorParameterWrong, "couldn't sign message:"+err.Error())
	}
	req.Signature = sig
	return c.SendProtobuf(si, req, nil)
}

// DelFollow asks the conode to remove a skipchain-id from the list of skipchains that are
// used to to allow/disallow new blocks. Only if SettingAuthentication(true) has
// been called is this active.
func (c *Client) DelFollow(si *network.ServerIdentity, clientPriv abstract.Scalar, sbid SkipBlockID) onet.ClientError {
	return nil
}

// ListFollow returns the list of latest skipblock of all skipchains that are followed
// for authentication purposes.
func (c *Client) ListFollow(si *network.ServerIdentity) (*ListFollowReply, onet.ClientError) {
	return nil, nil
}
