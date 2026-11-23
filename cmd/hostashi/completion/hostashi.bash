# SPDX-License-Identifier: AGPL-3.0-or-later
#
# bash completion for hostashi, printed by "hostashi completion bash".
# The candidates come from "hostashi __complete WORD... CURRENT".

_hostashi() {
  local cur words cword
  if declare -F _get_comp_words_by_ref >/dev/null; then
    # Keep key=value and host:port as one word.
    _get_comp_words_by_ref -n =: cur words cword
  else
    cur=${COMP_WORDS[COMP_CWORD]} words=("${COMP_WORDS[@]}") cword=$COMP_CWORD
  fi

  local out line
  out=$("${words[0]}" __complete "${words[@]:1:cword-1}" "$cur" 2>/dev/null) || return
  if [ "$out" = :files ]; then
    compopt -o default 2>/dev/null
    COMPREPLY=()
    return
  fi

  COMPREPLY=()
  while IFS= read -r line; do
    [ -n "$line" ] && COMPREPLY+=("${line%%$'\t'*}")
  done <<<"$out"

  # A key= or -flag= waits for its value.
  if [ ${#COMPREPLY[@]} -eq 1 ] && [ "${COMPREPLY[0]%=}" != "${COMPREPLY[0]}" ]; then
    compopt -o nospace 2>/dev/null
  fi
}

complete -F _hostashi hostashi
