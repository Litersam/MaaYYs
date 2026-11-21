package team_builder

import (
	maa "github.com/MaaXYZ/maa-framework-go/v4"
	"github.com/rs/zerolog/log"
)

// TeamBuilder reserves the legacy registration for external resource packs.
// The removed demo never implemented native party assembly.
type TeamBuilder struct{}

func (*TeamBuilder) Run(*maa.Context, *maa.CustomActionArg) bool {
	log.Warn().Msg("TeamBuilder 已停用，请使用 PartyCreateAndInvite 或 PartyWaitAndAccept")
	return false
}
