# Set version, OS, and Architecture
VERSION="0.4.59"
OS=$(uname -s)
ARCH=$(uname -m)

# Create the installation directories
mkdir -p "$HOME/.fly/bin"

# Download the tarball directly from GitHub Releases
curl -L "https://github.com/superfly/flyctl/releases/download/v${VERSION}/flyctl_${VERSION}_${OS}_${ARCH}.tar.gz" -o /tmp/flyctl.tar.gz
# Extract and move to your ~/.fly/bin directory
tar -C /tmp -xzf /tmp/flyctl.tar.gz
mv /tmp/flyctl "$HOME/.fly/bin/flyctl"
ln -sf "$HOME/.fly/bin/flyctl" "$HOME/.fly/bin/fly"

# Clean up
rm /tmp/flyctl.tar.gz

# Add it to your current shell session PATH
export PATH="$HOME/.fly/bin:$PATH"

