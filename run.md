create run.sh invoking the app with air

terminate all the process listening in ports 8910, 5173 and 4096 found with lsof -i

trap the ^c; when you press ^c:

launch air in background

launch kubefwd in background using kubeconfig ~/.ops/tmp/kubeconfig and namespace -n nuvolaris

open the browser `ops trustable signin http://localhost:8910`

wait until you press ^c and terminate everything

