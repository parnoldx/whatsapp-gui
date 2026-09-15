#!/bin/sh
# Stub helper for tests/pipe_test.go — speaks the daemon protocol.
# The first answer carries a push line in the SAME write, which is the case
# that used to be dropped when the GUI cleared its read buffer per job.
[ "$1" = "--daemon" ] || exit 0
first=1
while IFS= read -r line; do
  if [ "$first" = 1 ]; then
    first=0
    printf '{"ok":true,"data":{"chats":[]}}\n{"ok":true,"push":"link-preview","data":{"url":"https://example.com","title":"Example"}}\n'
  else
    printf '{"ok":true,"data":{"authenticated":true,"connected":true}}\n'
  fi
done
