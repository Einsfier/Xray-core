package dnscircuit

import (
	"context"

	"github.com/xtls/xray-core/common/errors"
)

func newError(values ...interface{}) *errors.Error {
	return errors.New(values...)
}

func logDebug(msg string) {
	errors.LogDebug(context.Background(), msg)
}

func logWarn(msg string) {
	errors.LogWarning(context.Background(), msg)
}
