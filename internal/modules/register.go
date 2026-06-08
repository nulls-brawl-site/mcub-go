package modules

import "github.com/nulls-brawl-site/mcub-go/internal/loader"

// AllSystemModules returns all built-in system modules. These are loaded into
// the kernel's SystemModules registry at startup and cannot be unloaded.
func AllSystemModules() []loader.Module {
	return []loader.Module{
		newCoreModule(),      // ping (Go built-in), restart, info
		newLoaderModule(),    // man, iload, um, reload
		newTesterModule(),    // ping (tester), logs, freezing, teaser
		newUpdatesModule(),   // restart (updates), update, stop
		newInfoModule(),      // info (MCUB_info)
		newTerminalModule(),  // t, tkill, ti
		newManModule(),       // man, manhide, manunhide, help
		newTrustedModule(),   // trust, untrust, trustlist, trustaccess, ...
		newAPIProtModule(),   // api_protection, api_reset, api_suspend, lockdown
		newTrModule(),        // tr, trlang
		newSettingsModule(),  // setprefix, addalias, delalias, aliases, lang, cleardb, clearmodules, clearcache, mcubinfo, piped, mcub
		newEvalModule(),      // py
		newConfigModule(),    // cfg, fcfg
		newLogBotModule(),    // log_setup, log_entries
		newBackupModule(),    // backup, restore, restore_with
		newPipedModule(),     // echo, grep, head, tail, sort, uniq, wc, calc, sed, strip, b64, jq, sleep, delete
		newCommandModule(),   // botsetup, setlang
		newPaymentsModule(),
	}
}
