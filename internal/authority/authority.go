package authority

import "fmt"

type Level uint8

const (
	Execution Level = iota + 1
	Architectural
	Human
)

func (l Level) String() string {
	switch l {
	case Execution:
		return "execution"
	case Architectural:
		return "architectural"
	case Human:
		return "human"
	default:
		return fmt.Sprintf("authority(%d)", l)
	}
}

type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type Decision struct {
	Level          Level    `json:"level"`
	Subject        string   `json:"subject"`
	Reason         string   `json:"reason"`
	Recommendation string   `json:"recommendation,omitempty"`
	Options        []Option `json:"options,omitempty"`
}

func (d Decision) RequiresHuman() bool { return d.Level == Human }
