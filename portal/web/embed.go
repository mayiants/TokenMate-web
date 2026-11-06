package web

import "embed"

//go:embed templates/* templates/admin/* templates/auth/* templates/billing/* templates/enterprise/* templates/errors/* templates/partials/* templates/super/* static/* static/css/* static/fonts/* static/js/* i18n/*
var FS embed.FS
