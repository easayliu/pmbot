# pmbot

Polymarket 5-minute BTC Up/Down trading bot.

## Install

### From Release (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/easayliu/pmbot/main/install.sh | sudo bash
```

Install a specific version:

```bash
curl -fsSL https://raw.githubusercontent.com/easayliu/pmbot/main/install.sh | sudo VERSION=v0.1.0 bash
```

### From Source

```bash
git clone https://github.com/easayliu/pmbot.git
cd pmbot
make build        # or: make build-upx
sudo bash install.sh
```

## Configuration

```bash
sudo vi /etc/pmbot/env          # set POLYMARKET_PRIVATE_KEY
sudo vi /etc/pmbot/config.yaml  # trading parameters
```

## Usage

```bash
sudo systemctl start pmbot      # start
sudo systemctl status pmbot     # status
sudo journalctl -u pmbot -f     # logs
```

## Development

```bash
make build       # build linux/amd64
make build-upx   # build + UPX compress
make test        # run tests
make lint        # go vet
```
