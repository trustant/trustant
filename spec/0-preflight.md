When starting, before anything else:

- if there is a file workgroup/pgid read it and terminate the process group and remove the file
- use lsof -i and check for processes occopying in port 8910 4096 and 5173 and kill them
- check the health of ollama: 
1. looking ad the configuration in opencode.json
check the server listed in provider.ollama.options.baseURL
and the models listed as keys in provider.ollama.models
2. check if the baseURL (remove the path) is returning Ollama is runnig
3. for each model execute a simple non streamin request "hello" 
4. wait up to 60 seconds,
5. if you do not receive an answer  restart the container ollama executing
`docker restart ollama`
6. retry once from step 1, othewise wait until you get Ollama is running, otherwise terminate with an error
7. if the url invoked is in format: `<protocol>://<ip-address>[:<port>]` redirect to 
` <protocol>://tru.<ip-address>[:<port>].nip.io`
 8. verify always it is invoked as `tru.<domain>[:<port>]` and if not show a error message saying "Please use <protocol>://<local-hostname>:[<port>]` where the <local-hosname> is the first field of command `hostname -I`

 