package cli

// Global holds flags that apply across every subcommand. Global flags are
// parsed once, before the subcommand name, e.g.:
//
//	dockvault --home /srv/dockvault --verbose scan --auto
//
// Individual subcommands may also expose a same-named flag (e.g.
// `--dry-run` on backup/generate/restore) for convenience; where both are
// set, the subcommand-local flag wins.
type Global struct {
	Home         string // overrides $DOCKVAULT_HOME
	RcloneRemote string // overrides the configured rclone remote name
	Environment  string // overrides the remote path's <environment> segment (default "stage")
	DryRun       bool   // don't write files / don't execute backups
	Verbose      bool   // verbose logging

	ShowVersion bool
	ShowHelp    bool
}

// defaultGlobal returns a Global populated with dockvault's documented
// defaults, before flag parsing or config-file overrides are applied.
func defaultGlobal() Global {
	return Global{
		Home:         "", // resolved by workspace.Home(), default ~/dockvault_scripts
		RcloneRemote: "dockvault_backup",
		Environment:  "stage",
	}
}
