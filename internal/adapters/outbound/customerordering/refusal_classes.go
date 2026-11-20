package customerordering

type refusalClass uint8

const (
	classUnset refusalClass = iota
	classTerminal
	classRetryable
)

type refusalTable map[string]refusalClass

func (t refusalTable) retryable(slug string) bool { return t[slug] == classRetryable }

var placeOrderRefusals = refusalTable{}
