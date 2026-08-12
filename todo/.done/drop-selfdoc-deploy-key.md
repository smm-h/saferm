# Drop the stale selfdoc deploy key

selfdoc.json still carries a `deploy` section (cloudflare-pages, project saferm) after the
standalone docs-site retirement (the subdomain now 301s into the unified site). Any future
`selfdoc deploy` re-clobbers the redirect stub with a standalone site — this exact
re-clobber happened live on a sibling project. Remove the key per the fleet pattern; verify
no docs-deploy pipeline in .rlsbl/config.json references it.

Effort: minutes.
