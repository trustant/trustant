This file describes support git api
Put the code in the file `git.go`

# POST /api/git

`{
   "name": <name>
    "cmd" <command>
 }'


Change to the folder `<workspacedir>/workspace/<name>`
and execute the command `git <command>`
return the output

