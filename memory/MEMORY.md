# Project Memory — cool-storage-api

## Plans & Design Docs

- [Upload Fix Plan](project_upload_fix_plan.md) — LibraryWriteCoordinator + preflush plan for fixing concurrent upload failures and double-time performance
- [Local dev stack startup](project_local_stack.md) — always use ENV_FILE=.env.example with --env-file flag; host port 3000 may conflict with devspace

## Testing

- [Test commands](feedback_test_commands.md) — always use docker compose --profile test, never go test from host

## Local-Only Code (never push)

- [dev-login endpoint](project_dev_login.md) — handleDevLogin shortcut for local role-switching; full code to re-apply manually after rebase; must never be committed
