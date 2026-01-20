curl -sSL https://get.volta.sh| bash
curl -sSL https://raw.githubusercontent.com/voidint/g/master/install.sh | bash
volta install node
g install 1.25.6
go install github.com/air-verse/air@latest
curl -fsSL https://opencode.ai/install | bash
echo 'export PATH=$HOME/.opencode/bin:$PATH' >> ~/.bashrc
source ~/.bashrc
sudo apt-get install python3-virtualenv -y
sudo touch /.dockerenv
test -e .env.production || echo "Please create a .env.production file based on .env.production.dist and fill in the required environment variables."
