create run.sh invoking the app with air

trap the ^c; when you press ^c:

terminate air
terminate all the process listening in ports 5173 and 4096 found with lsof -i