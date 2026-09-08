# Upgrading an existing cockpit

What changes between versions expect of a cockpit that already exists.

Nothing needs rebuilding, but two one-off housekeeping steps are worth doing
on a cockpit created before the caches were tightened:

```bash
chmod -R go-rwx <cockpit>/results <cockpit>/cache
```

New files under `results/` and `cache/dashboard/` are written `0600` inside
`0700` directories, but files already on disk keep the mode they were created
with until they are re-written. The command above strips all group and other
access from what is already there, in one pass. (Both directories are created
on demand, so on a cockpit that has never run a `check` or a `dashboard` there
is nothing to chmod and the command reports them missing — that is fine.) If
your cockpit is in version control, add `results/` and `cache/` to its ignore
file.

`.upkeep-env.yml` gained an optional `last_used_at` key (see
[Disk housekeeping](disk.md)). Existing environments parse
unchanged and need no rebuild.
