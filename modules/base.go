package modules

import (
	"github.com/SvenDH/recs/store"
)

type Name string
type Owner string

func RegisterBase(s *store.Store) {
	store.RegisterComponent[Name](s)
	store.RegisterComponent[Owner](s)
}
