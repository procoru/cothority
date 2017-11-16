package skipchain

/*
The `NewProtocol` method is used to define the protocol and to register
the handlers that will be called if a certain type of message is received.
The handlers will be treated according to their signature.

The protocol-file defines the actions that the protocol needs to do in each
step. The root-node will call the `Start`-method of the protocol. Each
node will only use the `Handle`-methods, and not call `Start` again.
*/

import (
	"errors"

	"time"

	"gopkg.in/dedis/onet.v1"
	"gopkg.in/dedis/onet.v1/crypto"
	"gopkg.in/dedis/onet.v1/log"
	"gopkg.in/dedis/onet.v1/network"
)

func init() {
	onet.GlobalProtocolRegister(Name, NewProtocol)
}

// Protocol is used for different communications in the skipchain-service.
type Protocol struct {
	*onet.TreeNodeInstance

	ER          *ProtoExtendRoster
	ERReply     chan []ProtoExtendSignature
	ERFollowers *[]*SkipBlock

	GU      *ProtoGetUpdate
	GUReply chan *SkipBlock
	GUSbm   *SkipBlockMap
}

// NewProtocol initialises the structure for use in one round
func NewProtocol(n *onet.TreeNodeInstance) (onet.ProtocolInstance, error) {
	t := &Protocol{
		TreeNodeInstance: n,
		ERReply:          make(chan []ProtoExtendSignature),
		GUReply:          make(chan *SkipBlock),
	}
	return t, t.RegisterHandlers(t.HandleExtendRoster, t.HandleExtendRosterReply,
		t.HandleGetUpdate, t.HandleBlockReply)
}

// Start sends the Announce-message to all children
func (p *Protocol) Start() error {
	log.Lvl3("Starting Protocol")
	if p.ER != nil {
		return p.SendToChildren(p.ER)
	} else if p.GU != nil {
		return p.SendToChildren(p.GU)
	} else {
		return errors.New("no new message requested")
	}
}

// HandleExtendRoster uses the stored followers to decide if we want to accept
// to be part of the new roster.
func (p *Protocol) HandleExtendRoster(msg ProtoStructExtendRoster) error {
	defer p.Done()

	if p.ERFollowers == nil {
		return p.SendToParent(&ProtoExtendRosterReply{})
	}

	for i, sb := range *p.ERFollowers {
		t := onet.NewRoster([]*network.ServerIdentity{p.ServerIdentity(), sb.Roster.List[0]}).GenerateBinaryTree()
		pi, err := p.CreateProtocol(Name, t)
		if err != nil {
			log.Error(err)
			continue
		}
		pisc := pi.(*Protocol)
		pisc.GU = &ProtoGetUpdate{SBID: sb.Hash}
		if err := pi.Start(); err != nil {
			log.Error(err)
			continue
		}
		select {
		case sbNew := <-pisc.GUReply:
			if sbNew != nil {
				(*p.ERFollowers)[i] = sbNew
			}
		case <-time.After(time.Second):
			continue
		}
	}

	for _, sb := range *p.ERFollowers {
		for _, si := range sb.Roster.List {
			if si.Equal(msg.ServerIdentity) {
				sig, err := crypto.SignSchnorr(network.Suite, p.Private(), msg.Genesis)
				if err != nil {
					log.Error("couldn't sign genesis-block")
					return p.SendToParent(&ProtoExtendRosterReply{})
				}
				return p.SendToParent(&ProtoExtendRosterReply{Signature: &sig})
			}
		}
	}
	return p.SendToParent(&ProtoExtendRosterReply{})
}

// HandleExtendRosterReply checks if all nodes are OK to hold this new block.
func (p *Protocol) HandleExtendRosterReply(reply []ProtoStructExtendRosterReply) error {
	defer p.Done()

	var sigs []ProtoExtendSignature
	for _, r := range reply {
		if r.Signature == nil {
			sigs = []ProtoExtendSignature{}
			break
		}
		if crypto.VerifySchnorr(network.Suite, r.ServerIdentity.Public, p.ER.Genesis, *r.Signature) != nil {
			sigs = []ProtoExtendSignature{}
			break
		}
		sigs = append(sigs, ProtoExtendSignature{SI: r.ServerIdentity.ID, Signature: *r.Signature})
	}
	p.ERReply <- sigs
	return nil
}

// HandleGetUpdate searches for a skipblock and returns it if it is found.
func (p *Protocol) HandleGetUpdate(msg ProtoStructGetUpdate) error {
	defer p.Done()

	if p.GUSbm == nil {
		log.Error(p.ServerIdentity(), "no block stored in Sbm")
		return p.SendToParent(&ProtoBlockReply{})
	}

	sb, err := p.GUSbm.GetLatest(p.GUSbm.GetByID(msg.SBID))
	if err != nil {
		log.Error("couldn't get latest: " + err.Error())
		return err
	}
	return p.SendToParent(&ProtoBlockReply{SkipBlock: sb})
}

// HandleBlockReply contacts the service that a new block has arrived
func (p *Protocol) HandleBlockReply(msg ProtoStructBlockReply) error {
	defer p.Done()
	p.GUReply <- msg.SkipBlock
	return nil
}
