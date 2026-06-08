#!/bin/bash
# Legacy compatibility alias for the unified bootstrap wrapper.
exec "$(dirname "$0")/bootstrap.sh" "$@" multiregion
