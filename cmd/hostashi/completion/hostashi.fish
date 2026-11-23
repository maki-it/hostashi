# SPDX-License-Identifier: AGPL-3.0-or-later
#
# fish completion for hostashi, printed by "hostashi completion fish".
# The candidates come from "hostashi __complete WORD... CURRENT"; fish
# shows the help after the tab itself.

function __hostashi_complete
    set -l words (commandline -opc)
    set -l cur (commandline -ct)
    set -l out ($words[1] __complete $words[2..-1] $cur 2>/dev/null)
    if test "$out[1]" = :files
        __fish_complete_path $cur
        return
    end
    printf '%s\n' $out
end

complete -c hostashi -f -a '(__hostashi_complete)'
