#!/usr/bin/env bash
# Builds the Arch package inside an archlinux container (used by CI).
# makepkg refuses to run as root, so a build user is created.
set -euo pipefail
pacman -Syu --noconfirm --needed base-devel git go wl-clipboard
useradd -m builder || true
git config --global --add safe.directory '*'
cp -r "$PWD" /home/builder/src && chown -R builder /home/builder/src
# Tag builds check out a detached HEAD; give makepkg a branch to clone.
git -C /home/builder/src checkout -q -B makepkg-build
su builder -c "git config --global --add safe.directory '*' && cd /home/builder/src/packaging/arch && SYNCME_SRC=/home/builder/src makepkg -f --noconfirm"
mkdir -p dist && cp /home/builder/src/packaging/arch/*.pkg.tar.zst dist/
pacman -U --noconfirm dist/*.pkg.tar.zst
syncme version
pacman -Ql syncme-git
