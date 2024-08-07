package modules

import (
	"github.com/SvenDH/recs/cluster"
)

type Name string
type Owner string

func RegisterBase(s *cluster.Store) {
	cluster.RegisterComponent[Name](s, true, true, true)
	cluster.RegisterComponent[Owner](s, true, true, true)
}
