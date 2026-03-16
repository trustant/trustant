This file describes support publish api
Put the code in the file `publish.go`

# POST /api/publish

`{
   "name": <name>
 }'


Change to the folder `<workspacedir>/workspace/<name>`
Check if there is an `.env.<name>`
If not return an error as "not available for publishing``

Execute two commands setting the env var `WSK_CONFIG_FILE=/tmp/<name>.props`

Execute the command

`ops ide login`

If successful check the file pointed by $WSK_CONFIG_FILE exists
otherwise return error

Execute

`ops ide deploy`

If successful return a message "publishing ok`

Otherwiser retun error

Remove the file pointed by $WSK_CONFIG_FILE