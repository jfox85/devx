Placeholder for the Wails embedded-asset directory.

This spike intentionally embeds no frontend: every request (including /)
falls through assetserver.Options.Handler, which reverse-proxies to the
per-launch private DevX web server. The existing Svelte SPA is served from
the Go binary's own embedded web/dist — no second copy of the frontend.
