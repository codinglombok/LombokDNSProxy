# LombokDNSProxy — Filter Lists

This directory holds the downloaded and compiled blocklists used by the filter engine.

## Files

| File | Description | Auto-updated |
|---|---|---|
| `adguard.txt` | AdGuard DNS filter | ✅ every 24h |
| `stevenblack.txt` | Steven Black unified hosts | ✅ every 24h |
| `oisd-big.txt` | OISD Big list | ✅ every 24h |
| `urlhaus.txt` | URLhaus malware domains | ✅ every 24h |
| `easyprivacy.txt` | EasyPrivacy tracking protection | ✅ every 24h |
| `blocklist.bin` | Compiled DAFSA trie (binary, generated) | ✅ after update |
| `custom-block.txt` | Your custom blocked domains | ❌ manual |
| `custom-allow.txt` | Your custom allowed domains | ❌ manual |

## Manual management

```bash
# Add a blocklist from URL
lombok-dns import https://example.com/mylist.txt

# Add a single domain
echo "ads.mybadsite.com" >> lists/custom-block.txt
lombok-dns reload

# Refresh all lists now (without waiting for auto-update)
task lists:update

# Compile raw .txt lists to binary trie
task lists:compile
```

## Format support

Lists in any of these formats are accepted:

- **Bare domain**: `ads.example.com`
- **Hosts format**: `0.0.0.0 ads.example.com` or `127.0.0.1 ads.example.com`
- **Wildcard**: `*.doubleclick.net`
- **Regex** (enclosed in `/`): `/^ad[0-9]+\.example\.com$/`
- **AdBlock Plus**: `||ads.example.com^`
- Lines starting with `#` or `!` are treated as comments
