package ntree

import (
	"errors"
	"fmt"

	"github.com/dedis/kyber/sign/schnorr"
	"github.com/dedis/onet"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
)

func init() {
	// register network messages and protocol
	network.RegisterMessage(Message{})
	network.RegisterMessage(SignatureReply{})
	onet.GlobalProtocolRegister("NaiveTree", NewProtocol)
}

// Protocol implements the onet.ProtocolInstance interface
type Protocol struct {
	*onet.TreeNodeInstance
	// the message we want to sign (and the root node propagates)
	Message []byte
	// VerifySignature reflects whether the signature will be checked
	// 0 - never
	// 1 - verify the collective signature
	// 2 - verify all sub-signatures
	VerifySignature int
	// signature of this particular participant:
	signature *SignatureReply
}

// NewProtocol is used internally to register the protocol.
func NewProtocol(node *onet.TreeNodeInstance) (onet.ProtocolInstance, error) {
	p := &Protocol{
		TreeNodeInstance: node,
	}
	err := p.RegisterHandler(p.HandleSignRequest)
	if err != nil {
		return nil, fmt.Errorf("Couldn't register handler %v", err)
	}
	err = p.RegisterHandler(p.HandleSignBundle)
	if err != nil {
		return nil, fmt.Errorf("Couldn't register handler %v", err)
	}
	return p, nil
}

// Start implements onet.ProtocolInstance.
func (p *Protocol) Start() error {
	if !p.IsRoot() {
		return fmt.Errorf("Called Start() on non-root ProtocolInstance")
	}
	if len(p.Children()) < 1 {
		return errors.New("No children for root")
	}
	log.Lvl3("Starting ntree/naive")
	return p.HandleSignRequest(structMessage{p.TreeNode(),
		Message{p.Message, p.VerifySignature}})
}

// HandleSignRequest is a handler for incoming sign-requests. It's registered as
// a handler in the onet.Node.
func (p *Protocol) HandleSignRequest(msg structMessage) error {
	p.Message = msg.Msg
	p.VerifySignature = msg.VerifySignature
	signature, err := schnorr.Sign(p.Suite(), p.Random, p.Private(), p.Message)
	if err != nil {
		return err
	}
	// fill our own signature
	p.signature = &SignatureReply{
		Sig:   signature,
		Index: p.TreeNode().RosterIndex}
	if !p.IsLeaf() {
		for _, c := range p.Children() {
			err := p.SendTo(c, &msg.Message)
			if err != nil {
				return err
			}
		}
	} else {
		err := p.SendTo(p.Parent(), &SignatureBundle{ChildSig: p.signature})
		p.Done()
		return err
	}
	return nil
}

// HandleSignBundle is a handler responsible for adding the node's signature
// and verifying the children's signatures (verification level can be controlled
// by the VerifySignature flag).
func (p *Protocol) HandleSignBundle(reply []structSignatureBundle) error {
	log.Lvl3("Appending our signature to the collected ones and send to parent")
	var sig SignatureBundle
	sig.ChildSig = p.signature
	// at least n signature from direct children
	count := len(reply)
	for _, s := range reply {
		// and count how many from the sub-trees
		count += len(s.SubSigs)
	}
	sig.SubSigs = make([]*SignatureReply, count)
	for _, sigs := range reply {
		// Check only direct children
		// see https://github.com/dedis/cothority/issues/260
		if p.VerifySignature == 1 || p.VerifySignature == 2 {
			s := p.verifySignatureReply(sigs.ChildSig)
			log.Lvl3(p.Name(), "direct children verification:", s)
		}
		// Verify also the whole subtree
		if p.VerifySignature == 2 {
			log.Lvl3(p.Name(), "Doing Subtree verification")
			for _, sub := range sigs.SubSigs {
				s := p.verifySignatureReply(sub)
				log.Lvl3(p.Name(), "verifying subtree signature:", s)
			}
		}
		if p.VerifySignature == 0 {
			log.Lvl3(p.Name(), "Skipping signature verification..")
		}
		// add both the children signature + the sub tree signatures
		sig.SubSigs = append(sig.SubSigs, sigs.ChildSig)
		sig.SubSigs = append(sig.SubSigs, sigs.SubSigs...)
	}

	if !p.IsRoot() {
		p.SendTo(p.Parent(), &sig)
	} else {
		log.Lvl3("Leader got", len(reply), "signatures. Children:", len(p.Children()))
		p.Done()
	}
	return nil
}

func (p *Protocol) verifySignatureReply(sig *SignatureReply) string {
	if sig.Index >= len(p.Roster().List) {
		log.Error("Index in signature reply out of range")
		return "FAIL"
	}
	entity := p.Roster().List[sig.Index]
	var s string
	if err := schnorr.Verify(p.Suite(), entity.Public, p.Message, sig.Sig); err != nil {
		s = "FAIL"
	} else {
		s = "SUCCESS"
	}
	return s
}

// ----- network messages that will be sent around ------- //

// Message contains the actual message (as a slice of bytes) that will be individually signed
type Message struct {
	Msg []byte
	// Simulation purpose
	// see https://github.com/dedis/cothority/issues/260
	VerifySignature int
}

// SignatureReply contains a signature for the message
//   * SchnorrSig (signature of the current node)
//   * Index of the public key in the entitylist in order to verify the
//   signature
type SignatureReply struct {
	Sig   []byte
	Index int
}

// SignatureBundle represent the signature that one children will pass up to its
// parent. It contains:
//  * The signature reply of a direct child (sig + index)
//  * The whole set of signature reply of the child sub tree
type SignatureBundle struct {
	// Child signature
	ChildSig *SignatureReply
	// Child subtree signatures
	SubSigs []*SignatureReply
}

type structMessage struct {
	*onet.TreeNode
	Message
}

type structSignatureBundle struct {
	*onet.TreeNode
	SignatureBundle
}

// ---------------- end: network messages  --------------- //
