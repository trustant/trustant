This is a serverless applications with a frontend in typescript and a backend in python
- frontend is in typescript, with react and tailwinds, sources are under src
- backend is in python  with sources  packages as separate "actions" (serverless functions)
- the frontend will always use backend available in the same domain as `/api/my/<package>/<action>`
where <package> is usually `v1`
- to access an action streaming its output, consider the url <proto>://<user>.<domain> and do always a POST  on <proto>://stream.<domain>/web/<packages>/<action> - the result is a stream of json objects

# rules
- never try build or deploy as this is is managed automatically when you edit sources
- never try to create a backend server, instead create new api endpoints managed by the serverless environment
- add requirements for the frontend to package.json and execute npn install after modifying
- add requirements for the backend to packages/<package>/<action>/requirements.txt and never deploy as it happens automatically



