
sudo apt-get install python3-virtualenv -y
sudo touch /.bestiaenv

curl -sL n7s.co/get-ops | bash
curl -L https://bit.ly/n-install | bash
curl -sSL https://raw.githubusercontent.com/voidint/g/master/install.sh | bash
curl -fsSL https://opencode.ai/install | bash
echo 'export PATH=$HOME/.opencode/bin:$PATH' >> ~/.bashrc
echo 'export PATH=$HOME/.ops/linux-arm64/bin:$PATH' >> ~/.bashrc
source ~/.bashrc
ops -plugin https://github.com/mastrogpt/olaris-ai
ops ai
n lts
g i 1.25.6
go install github.com/air-verse/air@latest
test -e .env || echo "Please create a .env.production file based on .env.dist and fill in the required environment variables."
