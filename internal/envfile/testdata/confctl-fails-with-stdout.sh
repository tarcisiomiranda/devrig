#!/bin/sh
# Fake confctl: prints a secret on stdout and exits non-zero with an empty stderr.
echo '{"REGISTRY_TOKEN":"leaked-token-value"}'
exit 1
