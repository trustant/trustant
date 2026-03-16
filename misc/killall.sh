for i in 8910 4096 5173
do
  lsof -i :$i | awk 'NR>1{print $2}' | xargs kill -9
done
