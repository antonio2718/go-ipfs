package pin

import (

	//ds "github.com/jbenet/go-ipfs/Godeps/_workspace/src/github.com/jbenet/datastore.go"
	ds "github.com/jbenet/go-ipfs/Godeps/_workspace/src/github.com/jbenet/datastore.go"
	nsds "github.com/jbenet/go-ipfs/Godeps/_workspace/src/github.com/jbenet/datastore.go/namespace"
	"github.com/jbenet/go-ipfs/blocks/set"
	mdag "github.com/jbenet/go-ipfs/merkledag"
	"github.com/jbenet/go-ipfs/util"
)

var recursePinDatastoreKey = ds.NewKey("/local/pins/recursive/keys")
var directPinDatastoreKey = ds.NewKey("/local/pins/direct/keys")
var indirectPinDatastoreKey = ds.NewKey("/local/pins/indirect/keys")

type Pinner interface {
	IsPinned(util.Key) bool
	Pin(*mdag.Node, bool) error
	Unpin(util.Key, bool) error
	Flush() error
}

type pinner struct {
	recursePin set.BlockSet
	directPin  set.BlockSet
	indirPin   *indirectPin
	dserv      *mdag.DAGService
	dstore     ds.Datastore
}

func NewPinner(dstore ds.Datastore, serv *mdag.DAGService) Pinner {

	// Load set from given datastore...
	rcds := nsds.Wrap(dstore, recursePinDatastoreKey)
	rcset := set.NewDBWrapperSet(rcds, set.NewSimpleBlockSet())

	dirds := nsds.Wrap(dstore, directPinDatastoreKey)
	dirset := set.NewDBWrapperSet(dirds, set.NewSimpleBlockSet())

	nsdstore := nsds.Wrap(dstore, indirectPinDatastoreKey)
	return &pinner{
		recursePin: rcset,
		directPin:  dirset,
		indirPin:   NewIndirectPin(nsdstore),
		dserv:      serv,
		dstore:     dstore,
	}
}

func (p *pinner) Pin(node *mdag.Node, recurse bool) error {
	k, err := node.Key()
	if err != nil {
		return err
	}

	if recurse {
		if p.recursePin.HasKey(k) {
			return nil
		}

		p.recursePin.AddBlock(k)

		err := p.pinLinks(node)
		if err != nil {
			return err
		}
	} else {
		p.directPin.AddBlock(k)
	}
	return nil
}

func (p *pinner) Unpin(k util.Key, recurse bool) error {
	if recurse {
		p.recursePin.RemoveBlock(k)
		node, err := p.dserv.Get(k)
		if err != nil {
			return err
		}

		return p.unpinLinks(node)
	} else {
		p.directPin.RemoveBlock(k)
	}
	return nil
}

func (p *pinner) unpinLinks(node *mdag.Node) error {
	for _, l := range node.Links {
		node, err := l.GetNode(p.dserv)
		if err != nil {
			return err
		}

		k, err := node.Key()
		if err != nil {
			return err
		}

		p.recursePin.RemoveBlock(k)

		err = p.unpinLinks(node)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *pinner) pinIndirectRecurse(node *mdag.Node) error {
	k, err := node.Key()
	if err != nil {
		return err
	}

	p.indirPin.Increment(k)
	return p.pinLinks(node)
}

func (p *pinner) pinLinks(node *mdag.Node) error {
	for _, l := range node.Links {
		subnode, err := l.GetNode(p.dserv)
		if err != nil {
			// TODO: Maybe just log and continue?
			return err
		}
		err = p.pinIndirectRecurse(subnode)
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *pinner) IsPinned(key util.Key) bool {
	return p.recursePin.HasKey(key) ||
		p.directPin.HasKey(key) ||
		p.indirPin.HasKey(key)
}

func LoadPinner(d ds.Datastore, dserv *mdag.DAGService) (Pinner, error) {
	p := new(pinner)

	var err error
	p.recursePin, err = set.SetFromDatastore(d, recursePinDatastoreKey)
	if err != nil {
		return nil, err
	}
	p.directPin, err = set.SetFromDatastore(d, directPinDatastoreKey)
	if err != nil {
		return nil, err
	}

	p.indirPin, err = loadIndirPin(d, indirectPinDatastoreKey)
	if err != nil {
		return nil, err
	}

	p.dserv = dserv
	p.dstore = d

	return p, nil
}

func (p *pinner) Flush() error {
	recurse := p.recursePin.GetKeys()
	err := p.dstore.Put(recursePinDatastoreKey, recurse)
	if err != nil {
		return err
	}

	direct := p.directPin.GetKeys()
	err = p.dstore.Put(directPinDatastoreKey, direct)
	if err != nil {
		return err
	}

	err = p.dstore.Put(indirectPinDatastoreKey, p.indirPin.refCounts)
	if err != nil {
		return err
	}
	return nil
}