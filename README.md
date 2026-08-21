# macdisco

Fast disk usage explorer for macOS — a WizTree/WinDirStat-style analyzer that lives in your terminal.

```
 macdisco  /Users/you
 311.7 GB · 2,997,149 items · free 120.4 GB · disk usage · sort: size

  SIZE                     USED    ITEMS   NAME
  152.1 GB  ████████████   48.8%  1229393  Library/
   52.3 GB  ████░░░░░░░░   16.8%   184220  Documents/
   27.9 GB  ██░░░░░░░░░░    8.9%    59042  Desktop/
   10.0 GB  █░░░░░░░░░░░    3.2%    71310  .cache/
   ...
```

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/awitwicki/macdisco/main/install.sh | sh
```

The installer downloads a prebuilt binary for your CPU (Apple Silicon or Intel) from the latest GitHub release, or builds from source if no release exists yet.

## Use

```sh
macdisco ~        # analyze your home folder
macdisco /        # analyze the whole disk
macdisco -r ~     # non-interactive report (also used automatically when piped)
```

> **Tip:** for a complete scan of protected folders (Mail, Messages, Time Machine caches…), give your terminal **Full Disk Access** in System Settings → Privacy & Security.

### Keys

| Key | Action |
|-----|--------|
| `↑↓` / `j k` | move |
| `⏎` / `l` / `→` | open directory (on a file: reveal in Finder) |
| `⌫` / `h` / `←` | go up |
| `g` / `G` | jump to top / bottom |
| `t` | move to **Trash** (undoable — Finder "Put Back" works) |
| `d` | **delete permanently** (with confirmation) |
| `f` | reveal selection in Finder |
| `o` | open current folder in Finder |
| `r` | rescan current folder |
| `s` | sort by size / name / items |
| `a` | toggle disk usage ↔ apparent size |
| `?` | help |
| `q` | quit |

## Why it's fast

- Directory listing uses `getattrlistbulk(2)` — one syscall returns names, types and sizes for a whole batch of entries, instead of `readdir` + one `lstat` per file (the same trick that makes WizTree-class tools fast). Roughly 400k files/second on Apple Silicon.
- Directories are walked in parallel by a bounded worker pool.
- Hardlinked files are counted once; symlinks are not followed.
- On network filesystems without bulk support it transparently falls back to a portable scanner (`MACDISCO_NO_BULK=1` forces this).

When scanning `/`, virtual and duplicate volumes (`/System/Volumes/Data` — already visible through firmlinks — `/Volumes`, `/dev`, Preboot/VM) are skipped so nothing is double-counted.

## Build from source

```sh
git clone https://github.com/awitwicki/macdisco && cd macdisco
go build -o macdisco .
```

## Cutting a release

Push a version tag; GitHub Actions builds both architectures and publishes the release that `install.sh` picks up:

```sh
git tag v1.0.0 && git push origin v1.0.0
```

## License

MIT
