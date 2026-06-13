create run.sh invoking the app with air:

1. terminate all the process listening in ports 8910, 5173 and 4096 found with lsof -i

2. trap the ^c; when you press ^c

3. launch air in background

4. launch kubefwd in background using
- kubeconfig ~/.ops/tmp/kubeconfig
- namespace -n nuvolaris
- forward the following services: redis nuvolaris-milvus seaweedfs nuvolaris-postgres nuvolaris-mongodb-svc

5. open the browser `ops trustable signin http://localhost:8910`

6. wait until you press ^c and terminate everything

