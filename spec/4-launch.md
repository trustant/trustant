This file describes the api for launching.
Put the code in the file `launch.go`

# GET /api/launch/<name>

When invoking this api it should check the folder
<workspacedir>/workspace/<name> exists, if not and return

```
{ "error": <error> }
```

## terminate leftover processes

Then, check if exists a `<workspacedir>/workspace/pgid` file
invoke `DELETE /api/launch` to ensure the group is terminated

If it is stll there, forcefully terminate the process group
pointed by that file.

## login

Change to `<workspacedir>/workspace/<app>` folder
and execute `ops ide login`

If it terminates with 0 continue otherwise return error

## check ports

Assume `opencode` port will be 4096.

Assume `opsdevel`  port will be 5173.

Check if ports for `opencode` and `opsdevel` are free,
otherwise return error.

## copy opencode configuration files

Before starting opencode, always copy the opencode configuration files to the workspace directory, overwriting existing files:

If the file `~/.config/opencode/opencode.json` does not exist, generate it first by calling `generateOpencodeConfig()`.

Copy the file `~/.config/opencode/opencode.json` to `<workspacedir>/workspace/<app>/opencode.json`, overwriting existing files.

Write the embedded `opencode.md` to `<workspacedir>/workspace/<app>/opencode.md`, overwriting existing files.

## start process group

Let <directory> be the absolute path of `<workspacedir>/workspace/<app>`

Execute  opencode changing to this directory as

```
opencode serve --port 4096 --hostname 0.0.0.0 --log-level DEBUG --print-logs
```

with out and err in stdout and stderr.

Get its process group.

Ensure it does not terminate within .5 seconds
If it terminates return error

Write the process group in `<workspacedir>/workspace/pgid`

Execute `ops ide devel`  in <directory> using the same process group as opencode

Check the command does not terminate within .5 seconds.

If it terminates, kill the whole process group and remove  `<workspacedir>/workspace/pgid`, and return error

Wait that both the processes are up and running and ports are listening.

When ok, execute a POST to <domain>:4006/session/ with header "X-Opencode-Directory: <directory>" and log the result of this invocation,

then return:

`{
  "left" : <opencode-port>,
  "right": <opsdeve-port>,
  "b64dir": <base64-urlsafe-encoded directory>
  "encdir": <url-encoded directory>
}`

Base64-Url-Safe encode is as follows:
btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=/g, "")


# DELETE /api/launch

Terminate forcefully the process group you started
and written in `<workspacedir>/workspace/pgid`
Delete the file `<workspacedir>/workspace/pgid`.




