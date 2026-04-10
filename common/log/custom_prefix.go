package log

import "github.com/xtls/xray-core/common/serial"

// PrefixMessage logs a message with given prefix into errlogger, discarding its severity.
// If prefix is empty, default "Important" will be used.
type PrefixMessage struct {
	Prefix  string
	Content interface{}
}

func (m *PrefixMessage) String() string {
	if len(m.Prefix) <= 0 {
		return serial.Concat("[Important] ", m.Content)
	}
	return serial.Concat("["+m.Prefix+"] ", m.Content)
}
