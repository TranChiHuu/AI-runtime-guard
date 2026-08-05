#!/bin/sh
# Capture-only hook: records the raw payload and decides nothing.
#
# Exits 0 with no stdout, so it cannot allow, deny, or perturb the session.
# Its only purpose is to answer "what does the host actually send", which no
# amount of reading the docs can settle.
cat >> "${GUARD_CAPTURE:-/tmp/guard-capture.jsonl}"
echo >> "${GUARD_CAPTURE:-/tmp/guard-capture.jsonl}"
exit 0
