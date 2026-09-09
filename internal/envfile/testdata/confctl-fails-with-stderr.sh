#!/bin/sh
# Fake confctl: fails with a message on stderr and nothing useful on stdout.
echo 'bad thing' 1>&2
exit 1
