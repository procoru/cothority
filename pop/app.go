package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path"
	"strings"

	"github.com/dedis/cothority"
	"github.com/dedis/cothority/cosi/check"
	_ "github.com/dedis/cothority/cosi/protocol"
	_ "github.com/dedis/cothority/cosi/service"

	"github.com/BurntSushi/toml"
	"github.com/dedis/cothority/pop/service"
	"github.com/dedis/kyber"
	"github.com/dedis/kyber/sign/anon"
	"github.com/dedis/kyber/util/encoding"
	"github.com/dedis/kyber/util/key"
	"github.com/dedis/kyber/util/random"
	"github.com/dedis/onet"
	"github.com/dedis/onet/app"
	"github.com/dedis/onet/log"
	"github.com/dedis/onet/network"
	"gopkg.in/urfave/cli.v1"
)

func init() {
	network.RegisterMessage(Config{})
}

// Config represents either a manager or an attendee configuration.
type Config struct {
	// Public key of org. Used for linking and
	// org authentication
	OrgPublic kyber.Point
	// Private key of org. Used for authentication
	OrgPrivate kyber.Scalar
	// Address of the linked conode.
	Address network.Address
	// Map of Final statements of the parties.
	// indexed by hash of party desciption
	Parties map[string]*PartyConfig
	// config-file name
	name string
}

// PartyConfig represents local configuration of party
type PartyConfig struct {
	// Private key of attendee or organizer, depending on value
	// of Index.
	Private kyber.Scalar
	// Public key of attendee or organizer, depending on value of
	// index.
	Public kyber.Point
	// Index of the attendee in the final statement. If the index
	// is -1, then this pop holds an organizer.
	Index int
	// Final statement of the party.
	Final *service.FinalStatement
}

func main() {
	appCli := cli.NewApp()
	appCli.Name = "Proof-of-personhood party"
	appCli.Usage = "Handles party-creation, finalizing, pop-token creation, and verification"
	appCli.Version = "0.1"
	appCli.Commands = []cli.Command{}
	appCli.Commands = []cli.Command{
		commandOrg,
		commandAttendee,
		commandAuth,
		{
			Name:      "check",
			Aliases:   []string{"c"},
			Usage:     "Check if the servers in the group definition are up and running",
			ArgsUsage: "group.toml",
			Action: func(c *cli.Context) error {
				return check.Config(c.Args().First(), false)
			},
		},
	}
	appCli.Flags = []cli.Flag{
		cli.IntFlag{
			Name:  "debug,d",
			Value: 0,
			Usage: "debug-level: 1 for terse, 5 for maximal",
		},
		cli.StringFlag{
			Name:  "config,c",
			Value: "~/.config/cothority/pop",
			Usage: "The configuration-directory of pop",
		},
	}
	appCli.Before = func(c *cli.Context) error {
		log.SetDebugVisible(c.Int("debug"))
		return nil
	}
	appCli.Run(os.Args)
}

// links this pop to a cothority
func orgLink(c *cli.Context) error {
	log.Lvl3("Org: Link")
	if c.NArg() == 0 {
		log.Fatal("Please give an IP and optionally a pin")
	}
	cfg, client := getConfigClient(c)

	host, port, err := net.SplitHostPort(c.Args().First())
	if err != nil {
		return err
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		return err
	}
	addr := network.NewTCPAddress(fmt.Sprintf("%s:%s", addrs[0], port))
	pin := c.Args().Get(1)
	if err := client.PinRequest(addr, pin, cfg.OrgPublic); err != nil {
		if err.ErrorCode() == service.ErrorWrongPIN && pin == "" {
			log.Info("Please read PIN in server-log")
			return nil
		}
		return err
	}
	cfg.Address = addr
	log.Lvl3("Successfully linked with", addr)
	cfg.write()
	return nil
}

// sets up a configuration
func orgConfig(c *cli.Context) error {
	log.Lvl3("Org: Config")
	if c.NArg() < 1 {
		log.Fatal(`Please give pop_desc.toml and (optionaly)
		merge_party.toml`)
	}
	cfg, client := getConfigClient(c)
	if cfg.Address.String() == "" {
		log.Fatal("No address")
		return errors.New("No address found - please link first")
	}
	desc := &service.PopDesc{}
	pdFile := c.Args().First()
	buf, err := ioutil.ReadFile(pdFile)
	log.ErrFatal(err, "While reading", pdFile)
	err = decodePopDesc(string(buf), desc)
	log.ErrFatal(err, "While decoding", pdFile)
	if c.NArg() == 2 {
		mergeFile := c.Args().Get(1)
		buf, err = ioutil.ReadFile(mergeFile)
		log.ErrFatal(err, "While reading", mergeFile)
		desc.Parties, err = decodeGroups(string(buf))
		log.ErrFatal(err, "While decoding ", mergeFile)

		// Check that current party is included in merge config
		found := false
		for _, party := range desc.Parties {
			if service.Equal(desc.Roster, party.Roster) {
				found = true
				break
			}
		}
		if !found {
			log.Fatal("party is not included in merge config")
		}
	}
	hash := base64.StdEncoding.EncodeToString(desc.Hash())
	log.Lvlf2("Hash of config: %s", hash)
	log.ErrFatal(client.StoreConfig(cfg.Address, desc, cfg.OrgPrivate))
	if val, ok := cfg.Parties[hash]; !ok {
		kp := key.NewKeyPair(cothority.Suite, random.New())
		cfg.Parties[hash] = &PartyConfig{
			Index: -1,
			Final: &service.FinalStatement{
				Desc:      desc,
				Attendees: []kyber.Point{},
				Signature: []byte{},
			},
			Public:  kp.Public,
			Private: kp.Secret,
		}
	} else {
		val.Final.Desc = desc
	}
	cfg.write()
	return nil
}

// adds a public key to the list
func orgPublic(c *cli.Context) error {
	if c.NArg() < 2 {
		log.Fatal("Please give a public key and hash of a party")
	}
	log.Lvl3("Org: Adding public keys", c.Args().First())
	str := c.Args().First()
	if !strings.HasPrefix(str, "[") {
		str = "[" + str + "]"
	}
	// TODO: better cleanup rules
	str = strings.Replace(str, "\"", "", -1)
	str = strings.Replace(str, "[", "", -1)
	str = strings.Replace(str, "]", "", -1)
	str = strings.Replace(str, "\\", "", -1)
	log.Lvl3("Niceified public keys are:\n", str)
	keys := strings.Split(str, ",")
	cfg, _ := getConfigClient(c)
	party, err := cfg.getPartybyHash(c.Args().Get(1))
	log.ErrFatal(err)
	for _, k := range keys {
		pub, err := encoding.String64ToPoint(cothority.Suite, k)
		if err != nil {
			log.Fatal("Couldn't parse public key:", k, err)
		}
		for _, p := range party.Final.Attendees {
			if p.Equal(pub) {
				log.Fatal("This key already exists")
			}
		}
		party.Final.Attendees = append(party.Final.Attendees, pub)
	}
	cfg.write()
	return nil
}

// finalizes the statement
func orgFinal(c *cli.Context) error {
	log.Lvl3("Org: Final")
	if c.NArg() < 1 {
		log.Fatal("Please give hash of pop-party")
	}
	cfg, client := getConfigClient(c)

	if len(cfg.Parties) == 0 {
		log.Fatal("No configs stored - first store at least one")
	}
	if cfg.Address == "" {
		log.Fatal("Not linked")
	}
	party, err := cfg.getPartybyHash(c.Args().First())
	log.ErrFatal(err)
	if len(party.Final.Signature) > 0 {
		finst, err := party.Final.ToToml()
		log.ErrFatal(err)
		log.Lvl2("Final statement already here:\n", "\n"+string(finst))
		return nil
	}
	fs, cerr := client.Finalize(cfg.Address, party.Final.Desc,
		party.Final.Attendees, cfg.OrgPrivate)
	log.ErrFatal(cerr)
	party.Final = fs
	cfg.write()
	finst, err := fs.ToToml()
	log.ErrFatal(err)
	log.Lvl2("Created final statement:\n", "\n"+string(finst))
	return nil
}

// sends Merge request
func orgMerge(c *cli.Context) error {
	log.Lvl3("Org:Merge")
	if c.NArg() < 1 {
		log.Fatal("Please give party-hash")
	}
	cfg, client := getConfigClient(c)
	if cfg.Address == "" {
		log.Fatal("Not linked")
	}
	party, err := cfg.getPartybyHash(c.Args().First())
	log.ErrFatal(err)
	if len(party.Final.Signature) <= 0 || party.Final.Verify() != nil {
		log.Lvl2("The local config is not finished yet")
		log.Lvl2("Fetching final statement")
		fs, err := client.FetchFinal(cfg.Address, party.Final.Desc.Hash())
		log.ErrFatal(err)
		if len(fs.Signature) <= 0 || fs.Verify() != nil {
			log.Fatal("Fetched final statement is invalid")
		}
		party.Final = fs
		cfg.write()
	}
	if party.Final.Merged {
		finst, err := party.Final.ToToml()
		log.ErrFatal(err)
		log.Lvl1("Merged final statement:\n", "\n"+string(finst))
		return nil
	}
	if len(party.Final.Desc.Parties) <= 0 {
		log.Fatal("there is no parties to merge")
	}

	fs, err := client.Merge(cfg.Address, party.Final.Desc, cfg.OrgPrivate)
	if err != nil {
		return err
	}
	party.Final = fs
	cfg.write()
	finst, err := fs.ToToml()
	log.ErrFatal(err)
	log.Lvl1("Created merged final statement:\n", "\n"+string(finst))
	return nil
}

// creates a new private/public pair
func attCreate(c *cli.Context) error {
	kp := key.NewKeyPair(cothority.Suite, random.New())
	secStr, err := encoding.ScalarToString64(nil, kp.Secret)
	if err != nil {
		return err
	}
	pubStr, err := encoding.PointToString64(nil, kp.Public)
	if err != nil {
		return err
	}
	log.Printf("Private: %s\nPublic: %s", secStr, pubStr)
	return nil
}

// joins a poparty
func attJoin(c *cli.Context) error {
	log.Lvl3("att: join")
	if c.NArg() < 2 {
		log.Fatal("Please give private key and final.toml")
	}
	privStr := c.Args().First()
	privBuf, err := base64.StdEncoding.DecodeString(privStr)
	log.ErrFatal(err)
	priv := cothority.Suite.Scalar()
	log.ErrFatal(priv.UnmarshalBinary(privBuf))
	cfg, client := getConfigClient(c)

	finalName := c.Args().Get(1)
	buf, err := ioutil.ReadFile(finalName)
	log.ErrFatal(err)
	final, err := service.NewFinalStatementFromToml(buf)
	log.ErrFatal(err)
	if len(final.Signature) <= 0 || final.Verify() != nil {
		log.Lvl2("The local config is not finished yet")
		if cfg.Address != "" {
			log.Lvl2("Fetching final statement")
			// Need to get the updated version of party config
			// Cause attendee doesn't know,
			// whether it has finished successfully or not
			fs, err := client.FetchFinal(cfg.Address, final.Desc.Hash())
			log.ErrFatal(err)
			if len(fs.Signature) <= 0 || fs.Verify() != nil {
				log.Fatal("Fetched final statement is invalid")
			}
			final = fs
		} else {
			log.Fatal("No address of conode to download final statement from")
		}
	}

	if len(final.Desc.Parties) > 0 && !final.Merged {
		log.Lvl2("The local party is not merged yet")
		if cfg.Address != "" {
			log.Lvl2("Fetching final statement")
			fs, err := client.FetchFinal(cfg.Address, final.Desc.Hash())
			log.ErrFatal(err)
			if !fs.Merged {
				log.Fatal("Global party is not merged")
			}
			if len(fs.Signature) <= 0 || fs.Verify() != nil {
				log.Fatal("Fetched final statement is invalid")
			}
			final = fs
		} else {
			log.Fatal("No address of conode to download final statement from")
		}

	}
	party := &PartyConfig{}
	party.Final = final
	party.Private = priv
	party.Public = cothority.Suite.Point().Mul(priv, nil)
	index := -1
	for i, p := range party.Final.Attendees {
		if p.Equal(party.Public) {
			log.Lvl1("Found public key at index", i)
			index = i
		}
	}
	if index == -1 {
		log.Fatal("Didn't find our public key in the final statement!")
	}
	party.Index = index
	hash := base64.StdEncoding.EncodeToString(final.Desc.Hash())
	log.Lvlf2("Final statement hash: %s", hash)
	if !c.Bool("yes") {
		fmt.Printf("Is it correct hash(y/n)")
		for {
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			c := strings.ToLower(string([]byte(input)[0]))
			if c == "n" {
				return nil
			} else if c != "y" {
				fmt.Printf("Please type (y/n)")
			} else {
				break
			}
		}
	}
	cfg.Parties[hash] = party
	cfg.write()
	log.Lvl3("Stored final statement")
	return nil
}

// signs a message + context
func attSign(c *cli.Context) error {
	log.Lvl3("att: sign")
	cfg, _ := getConfigClient(c)
	if c.NArg() < 3 {
		log.Fatal("Please give msg, context and party hash")
	}
	log.Lvl3("hash:", c.Args().Get(2))
	party, err := cfg.getPartybyHash(c.Args().Get(2))
	log.ErrFatal(err)

	if party.Index == -1 || party.Private == nil || party.Public == nil ||
		!cothority.Suite.Point().Mul(party.Private, nil).Equal(party.Public) {
		log.Fatal("No public key stored. Please join a party")
	}

	if len(party.Final.Signature) < 0 || party.Final.Verify() != nil {
		log.Fatal("Party is not finilized or signature is not valid")
	}

	msg := []byte(c.Args().First())
	ctx := []byte(c.Args().Get(1))
	Set := anon.Set(party.Final.Attendees)
	sigtag := anon.Sign(cothority.Suite.(anon.Suite), random.New(), msg,
		Set, ctx, party.Index, party.Private)
	sig := sigtag[:len(sigtag)-service.SIGSIZE/2]
	tag := sigtag[len(sigtag)-service.SIGSIZE/2:]
	log.Lvlf2("\nSignature: %s\nTag: %s", base64.StdEncoding.EncodeToString(sig),
		base64.StdEncoding.EncodeToString(tag))
	return nil
}

// verifies a signature and tag
func attVerify(c *cli.Context) error {
	log.Lvl3("att: verify")
	cfg, _ := getConfigClient(c)
	if c.NArg() < 5 {
		log.Fatal("Please give a msg, context, signature, a tag and party hash")
	}
	party, err := cfg.getPartybyHash(c.Args().Get(4))
	log.ErrFatal(err)

	if len(party.Final.Signature) < 0 || party.Final.Verify() != nil {
		log.Fatal("Party is not finilized or signature is not valid")
	}

	msg := []byte(c.Args().First())
	ctx := []byte(c.Args().Get(1))
	sig, err := base64.StdEncoding.DecodeString(c.Args().Get(2))
	log.ErrFatal(err)
	tag, err := base64.StdEncoding.DecodeString(c.Args().Get(3))
	log.ErrFatal(err)
	sigtag := append(sig, tag...)
	ctag, err := anon.Verify(cothority.Suite.(anon.Suite), msg,
		anon.Set(party.Final.Attendees), ctx, sigtag)
	log.ErrFatal(err)
	if !bytes.Equal(tag, ctag) {
		log.Fatalf("Tag and calculated tag are not equal:\n%x - %x", tag, ctag)
	}
	log.Lvl3("Successfully verified signature and tag")
	return nil
}

func authStore(c *cli.Context) error {
	log.Lvl3("auth: store")
	cfg, _ := getConfigClient(c)
	if c.NArg() < 1 {
		log.Fatal("Please give a final.toml")
	}

	finalName := c.Args().First()
	buf, err := ioutil.ReadFile(finalName)
	log.ErrFatal(err)
	final, err := service.NewFinalStatementFromToml(buf)
	log.ErrFatal(err)

	if len(final.Signature) <= 0 || final.Verify() != nil {
		log.Fatal("The local config is not finished yet")
	}

	if len(final.Desc.Parties) > 0 && !final.Merged {
		log.Fatal("The local party is not merged yet")
	}
	party := &PartyConfig{}
	party.Final = final
	hash := base64.StdEncoding.EncodeToString(final.Desc.Hash())
	cfg.Parties[hash] = party
	cfg.write()
	log.Lvlf1("Stored final statement, hash: %s", hash)
	return nil
}

// getConfigClient returns the configuration and a client-structure.
func getConfigClient(c *cli.Context) (*Config, *service.Client) {
	cfg, err := newConfig(path.Join(c.GlobalString("config"), "config.bin"))
	log.ErrFatal(err)
	return cfg, service.NewClient()
}

// newConfig tries to read the config and returns an organizer-
// config if it doesn't find anything.
func newConfig(fileConfig string) (*Config, error) {
	name := app.TildeToHome(fileConfig)
	if _, err := os.Stat(name); err != nil {
		kp := key.NewKeyPair(cothority.Suite, random.New())
		return &Config{
			OrgPublic:  kp.Public,
			OrgPrivate: kp.Secret,
			Parties:    make(map[string]*PartyConfig),
			name:       name,
		}, nil
	}
	buf, err := ioutil.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("couldn't read %s: %s - please remove it",
			name, err)
	}
	_, msg, err := network.Unmarshal(buf, cothority.Suite)
	if err != nil {
		return nil, fmt.Errorf("error while reading file %s: %s",
			name, err)
	}
	cfg, ok := msg.(*Config)
	if !ok {
		log.Fatal("Wrong data-structure in file", name)
	}
	if cfg.Parties == nil {
		cfg.Parties = make(map[string]*PartyConfig)
	}
	cfg.name = name
	return cfg, nil
}

// write saves the config to the given file.
func (cfg *Config) write() {
	buf, err := network.Marshal(cfg)
	log.ErrFatal(err)
	log.ErrFatal(ioutil.WriteFile(cfg.name, buf, 0660))
}

func (cfg *Config) getPartybyHash(hash string) (*PartyConfig, error) {
	if val, ok := cfg.Parties[hash]; ok {
		return val, nil
	}
	return nil, onet.NewClientErrorCode(service.ErrorInternal, "No such party")
}

// readGroup fetches group definition file.
func readGroup(name string) *onet.Roster {
	f, err := os.Open(name)
	log.ErrFatal(err, "Couldn't open group definition file")
	roster, err := app.ReadGroupToml(f, cothority.Suite)
	log.ErrFatal(err, "Error while reading group definition file", err)
	if len(roster.List) == 0 {
		log.ErrFatalf(err, "Empty entity or invalid group defintion in: %s",
			name)
	}
	return roster
}

// PopDescGroupToml represents serializable party description
type PopDescGroupToml struct {
	Name     string
	DateTime string
	Location string
	Servers  []*app.ServerToml `toml:"servers"`
}

func decodePopDesc(buf string, desc *service.PopDesc) error {
	descGroup := &PopDescGroupToml{}
	_, err := toml.Decode(buf, descGroup)
	if err != nil {
		return err
	}
	desc.Name = descGroup.Name
	desc.DateTime = descGroup.DateTime
	desc.Location = descGroup.Location
	entities := make([]*network.ServerIdentity, len(descGroup.Servers))
	for i, s := range descGroup.Servers {
		en, err := toServerIdentity(s, cothority.Suite)
		if err != nil {
			return err
		}
		entities[i] = en
	}
	desc.Roster = onet.NewRoster(entities)
	return nil
}

type shortDescGroupToml struct {
	Location string
	Servers  []*app.ServerToml `toml:"servers"`
}

// decode config of several groups into array of rosters
func decodeGroups(buf string) ([]*service.ShortDesc, error) {
	decodedGroups := make(map[string][]shortDescGroupToml)
	_, err := toml.Decode(buf, &decodedGroups)
	if err != nil {
		return []*service.ShortDesc{}, err
	}
	groups := decodedGroups["parties"]
	descs := []*service.ShortDesc{}
	for _, descGroup := range groups {
		desc := &service.ShortDesc{}
		desc.Location = descGroup.Location
		entities := make([]*network.ServerIdentity, len(descGroup.Servers))
		for j, s := range descGroup.Servers {
			en, err := toServerIdentity(s, cothority.Suite)
			if err != nil {
				return []*service.ShortDesc{}, err
			}
			entities[j] = en
		}
		desc.Roster = onet.NewRoster(entities)
		descs = append(descs, desc)
	}
	return descs, nil
}

// TODO: Needs to be public in app package!!!
// toServerIdentity converts this ServerToml struct to a ServerIdentity.
func toServerIdentity(s *app.ServerToml, suite kyber.Group) (*network.ServerIdentity, error) {
	public, err := encoding.String64ToPoint(suite, s.Public)
	if err != nil {
		return nil, err
	}
	si := network.NewServerIdentity(public, s.Address)
	si.Description = s.Description
	return si, nil
}
