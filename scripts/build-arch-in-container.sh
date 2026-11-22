#!/usr/bin/env bash
# Builds the Arch package inside an archlinux container (used by CI).
# makepkg refuses to run as root, so a build user is created.
set -euo pipefail
pacman -Syu --noconfirm --needed base-devel git go
useradd -m builder || true
git config --global --add safe.directory '*'
cp -r "$PWD" /home/builder/src && chown -R builder /home/builder/src
su builder -c "git config --global --add safe.directory '*' && cd /home/builder/src/packaging/arch && SYNCME_SRC=/home/builder/src makepkg -f --noconfirm"
mkdir -p dist && cp /home/builder/src/packaging/arch/*.pkg.tar.zst dist/
pacman -U --noconfirm dist/*.pkg.tar.zst
syncme version
pacman -Ql syncme-git
