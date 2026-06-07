#compdef lflow

local -a _1st_arguments

_1st_arguments=(
  'add:add a new note'
  'view:list books, notes, or view a content'
  'edit:edit a note or a book'
  'remove:remove a note or a book'
  'find:find notes by keywords'
  'sync:sync data with the server'
  'login:login to the lflow server'
  'logout:logout from the lflow server'
  'version:print the current version'
  'help:get help about any command'
)

get_booknames() {
  local names=$(lflow view --name-only)
  local -a ret

  while read -r line; do
    ret+=("${line}")
  done <<< "$names"

  echo "$ret"
}

if (( CURRENT == 2 )); then
  _describe -t commands "lflow subcommand" _1st_arguments
  return
elif (( CURRENT == 3 )); then
  case "$words[2]" in
    v|view|a|add)
      _alternative \
        "names:book names:($(get_booknames))"
  esac
fi

