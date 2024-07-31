package modules

import (
	"encoding/json"

	"github.com/SvenDH/recs/cluster"
//	"github.com/mlange-42/arche/ecs"
)

type Position struct {
	x float32
	y float32
	z float32
}

func (p *Position) UnmarshalJSON(d []byte) error {
    var tmp []json.RawMessage
    if err := json.Unmarshal(d, &tmp); err != nil {
        return err
    }
    if err := json.Unmarshal(tmp[0], &p.x); err != nil {
        return err
    }
    if err := json.Unmarshal(tmp[1], &p.y); err != nil {
        return err
    }
    if len(tmp) > 2 {
        if err := json.Unmarshal(tmp[2], &p.z); err != nil {
            return err
        }
    }
    return nil
}

func (p *Position) MarshalJSON() ([]byte, error) {
    return json.Marshal([]float32{p.x, p.y, p.z})
}

func RegisterPhysics(s *cluster.Server) {
	cluster.RegisterComponent[Position](s)
	//cluster.RegisterSystem(s, &Chat{})
}


