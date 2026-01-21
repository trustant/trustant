This file describes the backend api for repos.

Put the code in the file `repo.go`

# POST /api/repo
`{
  "name: <name>
  "repo": <repo>
  "password": <password>
  "apihost": <apihost>
}`

where <apihost> is optional

Performs the checks:

- `<name>` is alphanumericic, starts with a letter and is 6-20 letter log
- `<repo>` is in format `<user>/<path>`
-  list the users,  executing `ops admin listuser | awk 'NR>1{print $1}'` that returns the users, one per line and check the names does not exists
- check the folder `workspace/<name>` does not exist
- if <apihost> exists and is not empty check that `https://<apihost>/api/info` returns a json object with `description` == `OpenWhisk`

Return a descriptive error if it fails.

Tries to create the user with <name> and  <password> (8 alphanumeric characters) and store it in ~/.ops/<name>.password. Then create an user with:

`ops admin adduser <name> <name>@n7s.co <password> --all`

Return error if fails.

Then tries to clone the repo with:

`git clone git@github.com:<repo> workspace/<name>`

Return error if fails.

Then creates a  `workspace/<name>/.env`  with

```
OPS_USER=<name>
OPS_PASSWORD=<password>
OPS_APIHOST=http://miniops.me
OLLAMA_HOST=ollama:11434
OLLAMA_PROTO=http
OLLAMA_TOKEN=dummy
OPENAI_BASE_URL=http://ollama:11434/v1
OPENAI_API_KEY=dummy
OPENAI_MODEL=gpt-oss:20b
VITE_STREAM=http://stream.miniops.me
```

If <apihost> exists and it is defined create
a `workspace/<name>/.env.<name>` with

```
OPS_USER=<name>
OPS_PASSWORD=<password>
OPS_APIHOST=https://<apihost>
```

and append the content of the file `.env.production` in currrent directory.

Copy the file .env.production in current `workspace/<name>/.env.production`

Finally, if there is a `workspace/<name>/package.json`
execute:

```
cd workspace/<name>
npm install
```

Return success

# DELETE /api/repo
`{
  "name: <name>
}`

Check if there is the folder `workspace/<name>`

Delete the user with `ops admin deleteuser <name>`

Remove the folder  `workspace/<name>`

Remove the password from ~/.ops/<name>.password

# GET /api/repo

List the folders in workspace, each folder is a <name>
For each folder with a workspace/<name>/.git
read the remote origin git git
and use as <repo> the latest two parts of the (user/path)

Example: if remote is
git@github.com:nuvolaris/trustable-workspace
use as <repo> nuvolaris/trustable-workspace

If there is the file  workspace/<name>/.env.<name>,
read it and set the <apihost> to the value of `OPS_APIHOST=`,
removing the prefix `https`, otherwise <apihost> is empty

return an array of

`{
  "name: <name>
  "repo": <repo>
  "apihost" <apihost>
}`


# POST /api/upload

- accepting a multipart form-data with a file field `file` and a text field `name`
@- this commnd should accept an attached file 
- extact the <filename> from the file field and remove any path
- save it in workspace/<name>/upload/<filename>
- create the upload folder if necessary,
- overwrite exiting files with the same name
- return the complete absolute path of the uloaded file 200
- or 500 if any errpor

