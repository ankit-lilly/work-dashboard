package main

import (
	"embed"

	"github.com/EliLillyCo/work-dashboard/cmd"
	_ "time/tzdata"
)

//go:embed static/* static/**/*
var staticFS embed.FS

//go:embed schema.sql
var schemaSQL string

var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.SetStaticFS(staticFS)
	cmd.SetSchemaSQL(schemaSQL)
	cmd.Execute()
}
