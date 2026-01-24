This file describes the api for launching.
Put the code in the file `launch.go`

# GET /api/launch/<name>

When invoking this api it should check the folder
workspace/<name> exists, if not and return

```
{ "error": <error> }
```

## terminate leftover processes

Then, check if exists a `workspace/pgid` file
invoke `DELETE /api/lanuch` to ensure the group is terminated

If it is stll there, forcefully terminate the process group
pointed by that file.

## url encoded absolute path of the folder of the app

Calculate the  <url-encoded-absolute-path-of-app>

## configuration files

Copy the files `opencode.json` and `opencode.md` in the folder `workspace/<name>`, overwriting exiting files.

## create the .env

Retrieve the <password> with

```
ops util kubeget whiskuser/<name> .spec.password
```

Calulate the <streamer> looking at the `Host` header,
expects it to be `<protocol>://tru.<domain>[:<port>]/<path>`
and calculate the <streamer> as `<protocol>://stream.<domain>`

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

## login

Change to `workspace/<app>` folder
and execute `ops ide login`

If it terminates with 0 continue otherwise return error

## check ports

Assume `opencode` port will be 4096.

Assume `opsdevel`  port will be 8080,
unless there is a file `vite.config.js` or `vite.config.ts`,
in this case read it and search for `port: xxxxx`
and use the found value as opsdevel port.

Check if ports for `opencode` and `opsdevel` are free,
otherwise return error.

## start process group

Change to `workspace/<app>` folder.

Start opencode as

```
opencode serve --port 4096 --hostname 0.0.0.0 --log-level DEBUG --print-logs
```

with out and err in stdout and stderr and get its process group.

Ensure it does not terminate within .5 seconds
If it terminates return error

Write the process group in `workspace/pgid`

Execute `ops ide devel` and out in the same process group as opencode

Check the command does not terminate within .5 seconds.

If it terminates, kill the whole process group and remove  `workspace/pgid`, and return error

Wait that both the processes are up and running and ports are listening.

When ok, return

`{
  "left" : <opencode-port>,
  "right": <opsdeve-port>,
  "directory": <url-encoded-absolute-path-of-project>
}`

# DELETE /api/app

Terminate forcefully the process group you started
and written in `workspace/pgid`
Delete the file `workspace/pgid`.




