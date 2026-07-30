Currently forked at 1.11.3

To build:
```
docker run -it -v .:/tmp/nomad --platform linux/{arch} golang:1.26.5 bash
cd /tmp/nomad
make bootstrap
make deps
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.3/install.sh | bash
\. "$HOME/.nvm/nvm.sh"
nvm install 20
npm install -y yarn
PATH=/root/.local/share/pnpm/global/5/node_modules/\@pnpm/exe/:/root/.local/share/pnpm:$PATH
curl -fsSL https://get.pnpm.io/install.sh | bash -
apt update && apt install zip
make prerelease
TARGETS=linux_{arch} make release