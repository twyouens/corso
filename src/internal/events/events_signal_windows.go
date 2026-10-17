package events

import (
	"context"

	"github.com/twyouens/corso/src/pkg/logger"
)

func signalDump(ctx context.Context) {
	logger.Ctx(ctx).Error("cannot send signal on Windows")
}
