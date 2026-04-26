ssh -i ~/Library/Application\ Support/Trustable/id_ed25519 trustable@$(cat ~/Library/Application\ Support/Trustable/current.ip)  sudo cat /etc/rancher/k3s/k3s.yaml >~/.ops/tmp/kubeconfig
