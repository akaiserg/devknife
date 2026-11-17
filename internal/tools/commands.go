package tools

import (
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"devknife/internal/core"
)

// shellCmd is one entry of the command library. Commands are only shown and
// copied, never run: the user pastes them into a terminal.
//
// Placeholders look like {{port:3000}}: a name and a default. A name used
// more than once takes its default from the first occurrence that has one.
// Names start with a lowercase letter, so Go/Docker templates such as
// {{.Names}} are left alone.
type shellCmd struct {
	ID, Title, Desc, Cat string
	Any                  string // the same on macOS and Linux
	Mac, Linux           string // platform variants; "" = not available there
	Tags                 string // extra search words
	Danger               bool   // kills, deletes or discards something
}

const (
	cmdNetwork   = "Network"
	cmdProcesses = "Processes"
	cmdMemory    = "Memory"
	cmdDisk      = "Disk"
	cmdFiles     = "Files"
	cmdText      = "Text"
	cmdSystem    = "System"
	cmdGit       = "Git"
	cmdDocker    = "Docker"
	cmdSSH       = "SSH"
)

var cmdCategories = []string{cmdNetwork, cmdProcesses, cmdMemory, cmdDisk, cmdFiles, cmdText, cmdSystem, cmdGit, cmdDocker, cmdSSH}

// shellCmds is ordered by category, then by how often people need each one.
var shellCmds = []shellCmd{
	// Network
	{ID: "kill-port", Cat: cmdNetwork, Title: "Kill the process using a port", Danger: true,
		Desc: "Frees a port held by a server that didn't shut down. Only the process listening on the port is killed, not browsers or clients connected to it.",
		Any:  `lsof -ti tcp:{{port:3000}} -sTCP:LISTEN | xargs -r kill -9`, Tags: "free port stop server address already in use eaddrinuse"},
	{ID: "port-owner", Cat: cmdNetwork, Title: "See which process is using a port",
		Desc: "Shows the command, PID and user of whatever listens on the port.",
		Any:  `lsof -nP -iTCP:{{port:3000}} -sTCP:LISTEN`, Tags: "who pid listening"},
	{ID: "open-ports", Cat: cmdNetwork, Title: "List open ports",
		Desc: "Every TCP port something is listening on, with the process behind it. On Linux, sudo also shows other users' processes.",
		Mac:  `lsof -nP -iTCP -sTCP:LISTEN`, Linux: `sudo ss -tulpn`, Tags: "listening netstat services"},
	{ID: "connections", Cat: cmdNetwork, Title: "Show active connections",
		Desc: "Established TCP connections and the processes that own them.",
		Mac:  `lsof -nP -iTCP -sTCP:ESTABLISHED`, Linux: `ss -tnp state established`, Tags: "netstat established sockets"},
	{ID: "local-ip", Cat: cmdNetwork, Title: "Show your local IP address",
		Desc: "The address other devices on your network use to reach this machine. On macOS, en0 is usually Wi-Fi.",
		Mac:  `ipconfig getifaddr en0`, Linux: `hostname -I`, Tags: "lan ip address"},
	{ID: "public-ip", Cat: cmdNetwork, Title: "Show your public IP address",
		Desc: "Asks an external service which address your traffic comes from.",
		Any:  `curl -s https://ifconfig.me; echo`, Tags: "external wan ip"},
	{ID: "port-reachable", Cat: cmdNetwork, Title: "Check if a remote port is reachable",
		Desc: "Tries a TCP connection and reports whether it worked, without sending data.",
		Any:  `nc -zv {{host:example.com}} {{port:443}}`, Tags: "telnet firewall connect test"},
	{ID: "dns-lookup", Cat: cmdNetwork, Title: "Look up a domain's IP addresses",
		Desc: "Resolves a hostname through DNS. Add a record type such as MX or TXT at the end for other records.",
		Any:  `dig +short {{host:example.com}}`, Tags: "dns resolve nslookup"},
	{ID: "http-headers", Cat: cmdNetwork, Title: "Show a URL's HTTP response headers",
		Desc: "Sends a HEAD request and prints the status line and headers.",
		Any:  `curl -sI {{url:https://example.com}}`, Tags: "curl status redirect"},
	{ID: "http-timing", Cat: cmdNetwork, Title: "Time each phase of an HTTP request",
		Desc: "Breaks a request down into DNS, connect, TLS, first byte and total time.",
		Any:  `curl -o /dev/null -s -w 'dns %{time_namelookup}s  connect %{time_connect}s  tls %{time_appconnect}s  first byte %{time_starttransfer}s  total %{time_total}s\n' {{url:https://example.com}}`, Tags: "latency slow curl performance"},
	{ID: "flush-dns", Cat: cmdNetwork, Title: "Flush the DNS cache",
		Desc: "Forgets cached DNS answers, for example after changing /etc/hosts or a DNS record.",
		Mac:  `sudo dscacheutil -flushcache && sudo killall -HUP mDNSResponder`, Linux: `resolvectl flush-caches`, Tags: "dns cache hosts"},
	{ID: "serve-folder", Cat: cmdNetwork, Title: "Serve the current folder over HTTP",
		Desc: "Starts a quick static file server on the port. Press Ctrl+C to stop it.",
		Any:  `python3 -m http.server {{port:8000}}`, Tags: "web server static share files"},
	{ID: "download", Cat: cmdNetwork, Title: "Download a file",
		Desc: "Saves the file under its own name, following redirects and failing on HTTP errors.",
		Any:  `curl -fLO {{url:https://example.com/file.zip}}`, Tags: "wget curl fetch"},
	{ID: "wifi-password", Cat: cmdNetwork, Title: "Show a saved Wi-Fi password",
		Desc: "Reads the password of a network this machine has joined. macOS asks for your login password first.",
		Mac:  `security find-generic-password -wa '{{network:MyWiFi}}'`, Linux: `nmcli -s -g 802-11-wireless-security.psk connection show '{{network:MyWiFi}}'`, Tags: "wifi wireless ssid key"},

	// Processes
	{ID: "top-cpu", Cat: cmdProcesses, Title: "Top 10 processes by CPU",
		Desc: "A snapshot of what is using the processor most.",
		Mac:  `ps -Ao pid,pcpu,pmem,comm -r | head -n 11`, Linux: `ps -eo pid,pcpu,pmem,comm --sort=-pcpu | head -n 11`, Tags: "cpu usage slow busy"},
	{ID: "find-process", Cat: cmdProcesses, Title: "Find a process by name",
		Desc: "The PID and full command line of every process whose command matches.",
		Mac:  `pgrep -fl {{name:node}}`, Linux: `pgrep -fa {{name:node}}`, Tags: "pid ps grep running"},
	{ID: "kill-by-name", Cat: cmdProcesses, Title: "Stop processes by name", Danger: true,
		Desc: "Asks every process whose command line matches to quit. Add -9 after pkill to force it.",
		Any:  `pkill -f {{name:node}}`, Tags: "kill killall terminate"},
	{ID: "process-files", Cat: cmdProcesses, Title: "List what a process has open",
		Desc: "Files, sockets and pipes the process holds open.",
		Any:  `lsof -p {{pid:12345}}`, Tags: "pid open files sockets ports"},
	{ID: "file-users", Cat: cmdProcesses, Title: "See which process has a file open",
		Desc: "Useful when a file can't be deleted or a disk can't be ejected because it's in use.",
		Any:  `lsof {{file:/path/to/file}}`, Tags: "busy in use eject locked"},
	{ID: "run-background", Cat: cmdProcesses, Title: "Run a command that survives closing the terminal",
		Desc: "Starts the command in the background and writes its output to a log file.",
		Any:  `nohup {{command:./script.sh}} > {{log:out.log}} 2>&1 &`, Tags: "background daemon detach nohup"},
	{ID: "repeat", Cat: cmdProcesses, Title: "Re-run a command every 2 seconds",
		Desc: "Keeps the latest output on screen. Press Ctrl+C to stop.",
		Mac:  `while true; do clear; {{command:date}}; sleep 2; done`, Linux: `watch -n 2 '{{command:date}}'`, Tags: "watch loop monitor refresh"},

	// Memory
	{ID: "memory-usage", Cat: cmdMemory, Title: "Show memory use",
		Desc: "How much RAM is used and free right now.",
		Mac:  `top -l 1 -s 0 | grep PhysMem`, Linux: `free -h`, Tags: "ram free used"},
	{ID: "top-memory", Cat: cmdMemory, Title: "Top 10 processes by memory",
		Desc: "The processes holding the most RAM (RSS, in kilobytes).",
		Mac:  `ps -Ao pid,rss,pmem,comm -m | head -n 11`, Linux: `ps -eo pid,rss,pmem,comm --sort=-rss | head -n 11`, Tags: "ram usage leak"},
	{ID: "swap", Cat: cmdMemory, Title: "Show swap use",
		Desc: "How much memory has been moved to disk. Heavy swapping slows everything down.",
		Mac:  `sysctl vm.swapusage`, Linux: `swapon --show`, Tags: "swap virtual memory"},
	{ID: "memory-pressure", Cat: cmdMemory, Title: "Check memory pressure",
		Desc: "Whether the system is short of memory. On Linux, watch the si and so (swap in/out) columns.",
		Mac:  `memory_pressure | tail -n 1`, Linux: `vmstat 1 5`, Tags: "ram pressure swap"},

	// Disk
	{ID: "disk-free", Cat: cmdDisk, Title: "Show free disk space",
		Desc: "Size, used and available space for every mounted disk.",
		Any:  `df -h`, Tags: "space full storage"},
	{ID: "folder-sizes", Cat: cmdDisk, Title: "Size of each folder here, biggest last",
		Desc: "Totals for every subfolder of the current folder, hidden ones included.",
		Any:  `du -h -d 1 . 2>/dev/null | sort -h`, Tags: "du space usage directory"},
	{ID: "big-files", Cat: cmdDisk, Title: "Find the biggest files",
		Desc: "The 20 largest files over a size, anywhere under a folder.",
		Any:  `find {{dir:.}} -type f -size +{{size:100M}} -exec du -h {} + 2>/dev/null | sort -rh | head -n 20`, Tags: "large space cleanup"},
	{ID: "disks", Cat: cmdDisk, Title: "List disks and partitions",
		Desc: "Drives, partitions, file systems and where they're mounted.",
		Mac:  `diskutil list`, Linux: `lsblk -f`, Tags: "drives partitions volumes mount"},
	{ID: "disk-io", Cat: cmdDisk, Title: "Watch disk activity",
		Desc: "Reads and writes, refreshed every second. Press Ctrl+C to stop. On Linux it needs the sysstat package.",
		Mac:  `iostat -w 1`, Linux: `iostat -xz 1`, Tags: "io slow disk"},

	// Files
	{ID: "find-file", Cat: cmdFiles, Title: "Find files by name",
		Desc: "Case-insensitive search for files and folders whose name contains the text.",
		Any:  `find {{dir:.}} -iname '*{{name:report}}*' 2>/dev/null`, Tags: "search locate"},
	{ID: "search-text", Cat: cmdFiles, Title: "Search for text in files",
		Desc: "Every line containing the text, with file name and line number, skipping .git and node_modules.",
		Any:  `grep -rn --exclude-dir=.git --exclude-dir=node_modules '{{text:TODO}}' {{dir:.}}`, Tags: "grep find content"},
	{ID: "recent-files", Cat: cmdFiles, Title: "Files changed recently",
		Desc: "Files modified in the last N minutes under a folder.",
		Any:  `find {{dir:.}} -type f -mmin -{{minutes:60}} 2>/dev/null`, Tags: "modified recent changed"},
	{ID: "replace-text", Cat: cmdFiles, Title: "Replace text in every file", Danger: true,
		Desc:  "Edits files in place, skipping .git and node_modules. Commit or back up first; the text to find is a regular expression.",
		Mac:   `grep -rl --exclude-dir=.git --exclude-dir=node_modules '{{from:old}}' {{dir:.}} | xargs -r sed -i '' 's/{{from}}/{{to:new}}/g'`,
		Linux: `grep -rl --exclude-dir=.git --exclude-dir=node_modules '{{from:old}}' {{dir:.}} | xargs -r sed -i 's/{{from}}/{{to:new}}/g'`, Tags: "sed search replace rename"},
	{ID: "count-lines", Cat: cmdFiles, Title: "Count lines of code",
		Desc: "Lines per file for one extension, the biggest files and the total last.",
		Any:  `find {{dir:.}} -name '*.{{ext:go}}' -not -path '*/node_modules/*' -not -path '*/.git/*' -print0 | xargs -0 wc -l | sort -n | tail -n 15`, Tags: "loc wc lines"},
	{ID: "follow-log", Cat: cmdFiles, Title: "Follow a log file",
		Desc: "Prints new lines as they're written. Press Ctrl+C to stop.",
		Any:  `tail -f {{file:app.log}}`, Tags: "tail log watch"},
	{ID: "follow-log-filter", Cat: cmdFiles, Title: "Follow a log, only matching lines",
		Desc: "Like tail -f, but shows only lines containing the text.",
		Any:  `tail -f {{file:app.log}} | grep --line-buffered '{{text:ERROR}}'`, Tags: "tail grep errors"},
	{ID: "tar-create", Cat: cmdFiles, Title: "Compress a folder to .tar.gz",
		Desc: "Packs the folder into one compressed archive.",
		Any:  `tar -czf {{archive:archive.tar.gz}} {{dir:folder}}`, Tags: "zip archive compress backup"},
	{ID: "tar-extract", Cat: cmdFiles, Title: "Extract a .tar.gz archive",
		Desc: "Unpacks the archive into the current folder.",
		Any:  `tar -xzf {{archive:archive.tar.gz}}`, Tags: "unzip untar extract"},
	{ID: "checksum", Cat: cmdFiles, Title: "Checksum a file (SHA-256)",
		Desc: "Compare the result with the checksum a download page publishes.",
		Mac:  `shasum -a 256 {{file:file.zip}}`, Linux: `sha256sum {{file:file.zip}}`, Tags: "hash sha256 verify integrity"},
	{ID: "copy-file", Cat: cmdFiles, Title: "Copy a file's contents to the clipboard",
		Desc: "Handy for pasting a config or key into a form. On Linux it needs xclip.",
		Mac:  `pbcopy < {{file:file.txt}}`, Linux: `xclip -selection clipboard < {{file:file.txt}}`, Tags: "clipboard pbcopy paste"},
	{ID: "sync-folders", Cat: cmdFiles, Title: "Copy a folder, only what changed",
		Desc: "Mirrors new and changed files to the destination with progress. A trailing / on the source copies its contents, not the folder itself.",
		Any:  `rsync -avh --progress {{src:source/}} {{dst:backup/}}`, Tags: "rsync backup copy sync"},
	{ID: "folder-tree", Cat: cmdFiles, Title: "Show a folder tree",
		Desc: "Files and folders down to a depth, skipping .git.",
		Any:  `find {{dir:.}} -maxdepth {{depth:2}} -not -path '*/.git*' | sort`, Tags: "tree ls structure"},
	{ID: "open-folder", Cat: cmdFiles, Title: "Open this folder in the file manager",
		Desc: "Opens the current folder in Finder or your desktop's file manager.",
		Mac:  `open .`, Linux: `xdg-open .`, Tags: "finder explorer nautilus"},
	{ID: "delete-ds-store", Cat: cmdFiles, Title: "Delete .DS_Store files", Danger: true,
		Desc: "Removes the hidden folder-settings files macOS leaves everywhere, in this folder and below.",
		Mac:  `find . -name .DS_Store -type f -delete`, Tags: "cleanup finder junk"},

	// Text
	{ID: "dedupe-lines", Cat: cmdText, Title: "Remove duplicate lines, keep the order",
		Desc: "Prints each line the first time it appears. sort -u would reorder them.",
		Any:  `awk '!seen[$0]++' {{file:file.txt}}`, Tags: "unique uniq duplicates"},
	{ID: "frequent-lines", Cat: cmdText, Title: "Most frequent lines",
		Desc: "Counts identical lines and shows the 20 most common, for example in a log.",
		Any:  `sort {{file:file.txt}} | uniq -c | sort -rn | head -n 20`, Tags: "count histogram uniq"},
	{ID: "csv-column", Cat: cmdText, Title: "Print one column of a CSV file",
		Desc: "Splits each line on the separator and prints the column (1 is the first). Quoted fields that contain the separator aren't handled.",
		Any:  `awk -F'{{sep:,}}' '{print ${{column:1}}}' {{file:data.csv}}`, Tags: "awk cut column field"},
	{ID: "json-clipboard", Cat: cmdText, Title: "Pretty-print JSON from the clipboard",
		Desc: "Formats the JSON you copied. devknife json does this too, with exact error positions.",
		Mac:  `pbpaste | python3 -m json.tool`, Linux: `xclip -o -selection clipboard | python3 -m json.tool`, Tags: "json format clipboard"},

	// System
	{ID: "os-version", Cat: cmdSystem, Title: "Show the OS version",
		Desc: "Name, version and build of the operating system.",
		Mac:  `sw_vers`, Linux: `cat /etc/os-release`, Tags: "version release distro"},
	{ID: "uptime", Cat: cmdSystem, Title: "Uptime and load",
		Desc: "Time since the last boot, and the load average over 1, 5 and 15 minutes.",
		Any:  `uptime`, Tags: "load boot"},
	{ID: "cpu-info", Cat: cmdSystem, Title: "Show CPU model and cores",
		Desc: "Processor model and the number of logical cores.",
		Mac:  `sysctl -n machdep.cpu.brand_string hw.ncpu`, Linux: `lscpu`, Tags: "processor cores hardware"},
	{ID: "hardware", Cat: cmdSystem, Title: "Hardware overview",
		Desc: "Model, chip, memory and serial number on macOS; a hardware summary on Linux.",
		Mac:  `system_profiler SPHardwareDataType`, Linux: `sudo lshw -short`, Tags: "model serial specs"},
	{ID: "battery", Cat: cmdSystem, Title: "Battery status",
		Desc: "Charge level and whether the battery is charging.",
		Mac:  `pmset -g batt`, Linux: `upower -i "$(upower -e | grep BAT | head -n 1)"`, Tags: "power charge laptop"},
	{ID: "keep-awake", Cat: cmdSystem, Title: "Keep the computer awake",
		Desc: "Prevents sleep until you press Ctrl+C, for example during a long download.",
		Mac:  `caffeinate -dims`, Linux: `systemd-inhibit --what=idle:sleep sleep infinity`, Tags: "sleep caffeinate insomnia"},
	{ID: "services", Cat: cmdSystem, Title: "List running services",
		Desc: "Background services and agents that are loaded.",
		Mac:  `launchctl list`, Linux: `systemctl list-units --type=service --state=running`, Tags: "daemons systemd launchd"},
	{ID: "system-logs", Cat: cmdSystem, Title: "Follow the system log for an app or service",
		Desc: "Streams new log entries. Press Ctrl+C to stop.",
		Mac:  `log stream --predicate 'process == "{{process:Finder}}"'`, Linux: `journalctl -fu {{service:nginx}}`, Tags: "logs journal console"},
	{ID: "path", Cat: cmdSystem, Title: "Show your PATH, one folder per line",
		Desc: "The folders your shell searches for commands, in order.",
		Any:  `echo "$PATH" | tr ':' '\n'`, Tags: "environment variables"},
	{ID: "which", Cat: cmdSystem, Title: "Find every copy of a command",
		Desc: "Every match on your PATH, plus aliases and functions: explains which version runs.",
		Any:  `type -a {{command:python3}}`, Tags: "which where version"},
	{ID: "top-history", Cat: cmdSystem, Title: "Your most used commands",
		Desc: "Counts the commands in your shell history.",
		Any:  `fc -l 1 | awk '{print $2}' | sort | uniq -c | sort -rn | head -n 15`, Tags: "history stats"},
	{ID: "sudo-last", Cat: cmdSystem, Title: "Run the last command again with sudo",
		Desc: `For when a command failed with "permission denied".`,
		Any:  `sudo !!`, Tags: "permission denied root"},
	{ID: "reload-shell", Cat: cmdSystem, Title: "Reload your shell",
		Desc: "Starts a fresh login shell, picking up changes to .zshrc or .bashrc.",
		Any:  `exec "$SHELL" -l`, Tags: "zshrc bashrc profile source"},
	{ID: "random-secret", Cat: cmdSystem, Title: "Generate a random secret",
		Desc: "Random bytes as Base64, for passwords, API keys or session secrets.",
		Any:  `openssl rand -base64 {{bytes:32}}`, Tags: "password token key random"},
	{ID: "timestamps", Cat: cmdSystem, Title: "Current Unix time and UTC time",
		Desc: "Seconds since 1970, then the same moment in ISO 8601.",
		Any:  `date +%s; date -u +%Y-%m-%dT%H:%M:%SZ`, Tags: "epoch date time"},

	// Git
	{ID: "git-undo-commit", Cat: cmdGit, Title: "Undo the last commit, keep the changes",
		Desc: "Removes the commit but leaves its changes staged, ready to commit again.",
		Any:  `git reset --soft HEAD~1`, Tags: "revert uncommit reset"},
	{ID: "git-amend", Cat: cmdGit, Title: "Add staged changes to the last commit",
		Desc: "Rewrites the last commit with what you staged, keeping its message. Avoid it on commits you've already pushed.",
		Any:  `git commit --amend --no-edit`, Tags: "amend fixup forgot"},
	{ID: "git-discard", Cat: cmdGit, Title: "Discard all local changes", Danger: true,
		Desc: "Resets tracked files and deletes untracked files and folders. This can't be undone.",
		Any:  `git restore . && git clean -fd`, Tags: "reset clean throw away"},
	{ID: "git-prune-branches", Cat: cmdGit, Title: "Delete local branches that are merged", Danger: true,
		Desc: "Removes local branches already merged into the current one, keeping main, master and develop.",
		Any:  `git branch --merged | grep -vE '^[*+]|^ *(main|master|develop)$' | xargs -r git branch -d`, Tags: "cleanup branches merged"},
	{ID: "git-log-graph", Cat: cmdGit, Title: "Compact history graph",
		Desc: "The last 20 commits, one line each, with branches and tags.",
		Any:  `git log --oneline --graph --decorate -n 20`, Tags: "log history tree"},
	{ID: "git-find-commit", Cat: cmdGit, Title: "Find commits by message",
		Desc: "Searches commit messages on all branches, ignoring case.",
		Any:  `git log --all --oneline -i --grep='{{text:fix}}'`, Tags: "search log grep"},
	{ID: "git-pickaxe", Cat: cmdGit, Title: "Find the commit that added or removed some code",
		Desc: "Lists the commits that changed how often the text appears.",
		Any:  `git log -S '{{text:functionName}}' --oneline`, Tags: "pickaxe search history blame"},
	{ID: "git-last-commit", Cat: cmdGit, Title: "Files changed in the last commit",
		Desc: "The last commit's message and a summary of the files it changed.",
		Any:  `git show --stat HEAD`, Tags: "show diff stat"},
	{ID: "git-stash", Cat: cmdGit, Title: "Stash changes with a name",
		Desc: "Puts your changes aside under a message you'll recognize in git stash list.",
		Any:  `git stash push -m '{{message:wip}}'`, Tags: "stash save shelve"},
	{ID: "gh-switch-account", Cat: cmdGit, Title: "Switch GitHub CLI account",
		Desc: "Changes the account gh uses when you're logged in to more than one. With two accounts it switches to the other; with more it asks which. Add --user NAME to pick one directly, and run gh auth status to list them.",
		Any:  `gh auth switch`, Tags: "github gh account user login change"},

	// Docker
	{ID: "docker-ps", Cat: cmdDocker, Title: "Running containers, compact",
		Desc: "Name, status and ports of each running container.",
		Any:  `docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}'`, Tags: "containers list ps"},
	{ID: "docker-shell", Cat: cmdDocker, Title: "Open a shell in a container",
		Desc: "An interactive shell inside a running container. Use bash instead of sh if the image has it.",
		Any:  `docker exec -it {{container:my-app}} sh`, Tags: "exec bash enter"},
	{ID: "docker-logs", Cat: cmdDocker, Title: "Follow a container's logs",
		Desc: "The last 100 lines, then new ones as they arrive.",
		Any:  `docker logs -f --tail 100 {{container:my-app}}`, Tags: "logs tail"},
	{ID: "docker-stats", Cat: cmdDocker, Title: "Container CPU and memory use",
		Desc: "A snapshot of resource use per container.",
		Any:  `docker stats --no-stream`, Tags: "stats resources memory cpu"},
	{ID: "docker-disk", Cat: cmdDocker, Title: "Docker disk use",
		Desc: "Space used by images, containers, volumes and the build cache.",
		Any:  `docker system df`, Tags: "space images volumes"},
	{ID: "docker-prune", Cat: cmdDocker, Title: "Free the space Docker uses", Danger: true,
		Desc: "Deletes stopped containers and unused images, networks, volumes and build cache. Docker asks before deleting.",
		Any:  `docker system prune -a --volumes`, Tags: "cleanup prune space"},
	{ID: "docker-stop-all", Cat: cmdDocker, Title: "Stop all running containers", Danger: true,
		Desc: "Stops every running container.",
		Any:  `docker ps -q | xargs -r docker stop`, Tags: "stop kill all"},

	// SSH
	{ID: "ssh-keygen", Cat: cmdSSH, Title: "Create an SSH key",
		Desc: "A modern Ed25519 key pair in ~/.ssh. The comment helps you recognize the key later.",
		Any:  `ssh-keygen -t ed25519 -C '{{comment:you@example.com}}'`, Tags: "key generate github"},
	{ID: "ssh-public-key", Cat: cmdSSH, Title: "Copy your public SSH key",
		Desc: "Puts your public key on the clipboard, ready to paste into GitHub or a server.",
		Mac:  `pbcopy < ~/.ssh/id_ed25519.pub`, Linux: `xclip -selection clipboard < ~/.ssh/id_ed25519.pub`, Tags: "github public key clipboard"},
	{ID: "ssh-copy-key", Cat: cmdSSH, Title: "Install your SSH key on a server",
		Desc: "Adds your public key to the server's authorized keys, so you can log in without a password.",
		Any:  `ssh-copy-id {{user:user}}@{{host:server}}`, Tags: "authorized_keys passwordless login"},
	{ID: "ssh-tunnel", Cat: cmdSSH, Title: "Forward a remote port to your machine",
		Desc: "Makes a port on the server, such as a database, reachable on localhost. Press Ctrl+C to close it.",
		Any:  `ssh -N -L {{local_port:5432}}:localhost:{{remote_port:5432}} {{user:user}}@{{host:server}}`, Tags: "tunnel port forward database"},
	{ID: "scp-upload", Cat: cmdSSH, Title: "Copy a file to a server",
		Desc: "Uploads over SSH. Swap the two arguments to download instead.",
		Any:  `scp {{file:file.txt}} {{user:user}}@{{host:server}}:{{path:~/}}`, Tags: "upload copy transfer"},
}

func commandsTool() *core.Tool {
	return &core.Tool{
		ID: "commands", Name: "Shell Commands", Category: core.CatReference, Extra: true,
		Description: "Handy terminal commands with what they do: free a port, list open ports, check memory and disk use, find big files and more. Copy one, adjust its values and run it in your terminal.",
		Aliases:     []string{"cmds", "shell", "snippets", "cheatsheet"},
		Keywords:    []string{"bash", "terminal", "kill port", "lsof", "disk usage", "memory", "cheat sheet"},
		Input:       core.InputNone,
		// devknife cmds kill port → search "kill port"
		Args: []string{"search"},
		Options: []core.Option{
			{Key: "search", Label: "Search", Type: core.OptString, Placeholder: "kill port, memory, disk…", Short: "s",
				Help: "Words to look for in titles, descriptions and commands; an exact ID such as kill-port prints just that command"},
			{Key: "os", Label: "OS", Type: core.OptSelect, Default: "auto", Choices: []string{"auto", "macOS", "Linux", "all"},
				Help: "Which variant to show; auto is this computer's OS (Linux on Windows, for WSL and Git Bash)"},
			{Key: "category", Label: "Category", Type: core.OptSelect, Default: "all", Choices: append([]string{"all"}, cmdCategories...)},
			{Key: "set", Label: "Values", Type: core.OptString, Placeholder: "port=8080 host=example.com", Hidden: true,
				Help: `Values for placeholders such as port=8080, separated by spaces; quote values with spaces ("two words"). Defaults fill the rest`},
		},
		Run: runCommands,
	}
}

// cmdVariant is one runnable form of a command. Cmd keeps its {{name:default}}
// placeholders so the UI can fill them in as you type.
type cmdVariant struct {
	OS  string `json:"os,omitempty"` // "macOS" or "Linux"; empty when the same everywhere
	Cmd string `json:"cmd"`
}

type cmdItem struct {
	ID       string       `json:"id"`
	Title    string       `json:"title"`
	Desc     string       `json:"desc"`
	Category string       `json:"category"`
	Danger   bool         `json:"danger,omitempty"`
	Commands []cmdVariant `json:"commands"`
}

func runCommands(r core.Request) (*core.Result, error) {
	os := cmdOS(r.Str("os"))
	vals, err := parseCmdValues(r.Str("set"))
	if err != nil {
		return nil, err
	}
	query := strings.TrimSpace(r.Str("search"))
	data := map[string]any{"os": os, "total": len(shellCmds)}
	res := &core.Result{Lang: "bash", View: "commands", Data: data}

	// An exact ID prints just the command, ready to paste or pipe to pbcopy.
	for _, c := range shellCmds {
		if strings.EqualFold(query, c.ID) {
			vs := c.variants(os)
			if len(vs) == 0 {
				return nil, fmt.Errorf("%s has no %s version; try --os all", c.ID, os)
			}
			data["items"] = []cmdItem{c.item(vs)}
			if len(vs) == 1 {
				res.Output = fillCmd(vs[0].Cmd, vals) + "\n"
			} else {
				res.Output = cmdScript([]cmdItem{c.item(vs)}, vals, false)
			}
			return res, nil
		}
	}

	cat := r.Str("category")
	words := strings.Fields(strings.ToLower(query))
	if len(words) > maxCmdSearchWords {
		return nil, fmt.Errorf("search with up to %d words", maxCmdSearchWords)
	}
	type hit struct {
		item  cmdItem
		score int
	}
	var hits []hit
	for _, c := range shellCmds {
		if cat != "" && !strings.EqualFold(cat, "all") && !strings.EqualFold(cat, c.Cat) {
			continue
		}
		vs := c.variants(os)
		if len(vs) == 0 {
			continue
		}
		score, ok := c.match(words)
		if !ok {
			continue
		}
		hits = append(hits, hit{c.item(vs), score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	items := make([]cmdItem, len(hits))
	for i, h := range hits {
		items[i] = h.item
	}
	data["items"] = items
	if len(items) == 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("No command matches %q. Try fewer words, another category, or OS \"all\".", query))
		return res, nil
	}
	res.Output = cmdScript(items, vals, true)
	return res, nil
}

// cmdOS resolves the os option to "macOS", "Linux" or "all".
func cmdOS(v string) string {
	switch strings.ToLower(v) {
	case "macos":
		return "macOS"
	case "linux":
		return "Linux"
	case "all":
		return "all"
	}
	if runtime.GOOS == "darwin" {
		return "macOS"
	}
	return "Linux" // Linux, BSDs, and Windows users running WSL or Git Bash
}

func (c shellCmd) variants(os string) []cmdVariant {
	if c.Any != "" {
		return []cmdVariant{{Cmd: c.Any}}
	}
	var vs []cmdVariant
	if c.Mac != "" && (os == "macOS" || os == "all") {
		vs = append(vs, cmdVariant{OS: "macOS", Cmd: c.Mac})
	}
	if c.Linux != "" && (os == "Linux" || os == "all") {
		vs = append(vs, cmdVariant{OS: "Linux", Cmd: c.Linux})
	}
	if os != "all" && len(vs) == 1 {
		vs[0].OS = "" // the only variant for the chosen OS needs no label
	}
	return vs
}

func (c shellCmd) item(vs []cmdVariant) cmdItem {
	return cmdItem{ID: c.ID, Title: c.Title, Desc: c.Desc, Category: c.Cat, Danger: c.Danger, Commands: vs}
}

// match reports whether every word appears somewhere in the entry, and how
// well: words in the title count most, then the ID and tags.
func (c shellCmd) match(words []string) (int, bool) {
	title := strings.ToLower(c.Title)
	key := strings.ToLower(c.ID + " " + c.Tags + " " + c.Cat)
	rest := strings.ToLower(c.Desc + " " + c.Any + " " + c.Mac + " " + c.Linux)
	score := 0
	for _, w := range words {
		switch {
		case strings.Contains(title, w):
			score += 3
		case strings.Contains(key, w):
			score += 2
		case strings.Contains(rest, w):
			score++
		default:
			return 0, false
		}
	}
	return score, true
}

// cmdScript renders items as a shell script: a comment with the title and
// description, then the command, so the whole list can be pasted or saved.
func cmdScript(items []cmdItem, vals map[string]string, withDesc bool) string {
	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("# " + it.Title)
		if it.Danger {
			b.WriteString(" (careful)")
		}
		b.WriteByte('\n')
		if withDesc {
			b.WriteString("#   " + it.Desc + "\n")
		}
		for _, v := range it.Commands {
			if v.OS != "" {
				b.WriteString("# " + v.OS + ":\n")
			}
			b.WriteString(fillCmd(v.Cmd, vals) + "\n")
		}
	}
	return b.String()
}

// maxCmdSearchWords bounds the work a search costs: every word is checked
// against every command.
const maxCmdSearchWords = 20

var (
	reCmdParam = regexp.MustCompile(`\{\{([a-z][a-z0-9_]*)(?::([^{}]*))?\}\}`)
	reCmdName  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// fillCmd replaces each {{name:default}} placeholder with its value from
// vals, or its default. The UI does the same in fillCommand (app.js).
func fillCmd(tmpl string, vals map[string]string) string {
	defs := map[string]string{}
	for _, m := range reCmdParam.FindAllStringSubmatch(tmpl, -1) {
		if _, ok := defs[m[1]]; !ok && m[2] != "" {
			defs[m[1]] = m[2]
		}
	}
	return reCmdParam.ReplaceAllStringFunc(tmpl, func(s string) string {
		name := reCmdParam.FindStringSubmatch(s)[1]
		if v := vals[name]; v != "" {
			return v
		}
		return defs[name]
	})
}

// parseCmdValues reads `port=8080 msg="two words"`: name=value pairs separated
// by whitespace; a value may be quoted with "…" (backslash escapes) or '…'.
// The UI writes the same format (formatValues in app.js).
func parseCmdValues(s string) (map[string]string, error) {
	vals := map[string]string{}
	isSpace := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
	i := 0
	for {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			return vals, nil
		}
		eq := strings.IndexByte(s[i:], '=')
		end := i
		for end < len(s) && !isSpace(s[end]) {
			end++
		}
		if eq < 0 || i+eq > end {
			return nil, fmt.Errorf("values look like name=value, for example port=8080; got %q", s[i:end])
		}
		name := s[i : i+eq]
		if !reCmdName.MatchString(name) {
			return nil, fmt.Errorf("%q isn't a placeholder name: use lowercase letters, digits and _", name)
		}
		i += eq + 1
		var v strings.Builder
		if i < len(s) && (s[i] == '"' || s[i] == '\'') {
			q := s[i]
			i++
			closed := false
			for i < len(s) {
				c := s[i]
				if c == '\\' && q == '"' && i+1 < len(s) {
					v.WriteByte(s[i+1])
					i += 2
					continue
				}
				i++
				if c == q {
					closed = true
					break
				}
				v.WriteByte(c)
			}
			if !closed {
				return nil, fmt.Errorf("the value of %s is missing its closing %c", name, q)
			}
		} else {
			for i < len(s) && !isSpace(s[i]) {
				v.WriteByte(s[i])
				i++
			}
		}
		vals[name] = v.String()
	}
}
