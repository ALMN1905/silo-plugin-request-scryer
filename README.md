# Silo Scryer Requests

A [Silo](https://github.com/Silo-Server/silo-server) request router that sends approved requests to [Scryer](https://github.com/scryer-media/scryer).

## How it works

1. A request is approved in Silo, and Silo's routing sends it to a Scryer server.
2. The plugin adds the title to Scryer as **monitored** (or reuses it when Scryer already has it) and starts Scryer's automatic search. Scryer's quality profile chooses the release, and its download client fetches it.
3. Every few minutes Silo asks how the request is doing. The plugin reads the title's downloads and files in Scryer:

| Scryer | Silo shows |
|---|---|
| Downloads in flight | Downloading, with progress and an estimated finish |
| Files, nothing downloading | Completed |
| No files, nothing downloading | Searching |
| Unmonitored, no files | Searching, worded "Not monitored" |
| Title removed | Failed |

Silo also completes a request on its own once the title reaches its library.

Series are requested whole. Anime series go to Scryer's anime library.

**Series and anime monitoring** in the server's settings picks which episodes Scryer monitors for a requested series: *Leave to Scryer* (default), *All episodes*, *Missing and future episodes* or *Future episodes only* (no backlog download). With *Leave to Scryer* the plugin sends no choice: a new series has every episode monitored (Scryer does not apply the library's default monitoring to titles added through the API), and a series Scryer already has keeps its monitoring. Any other choice is also applied to a series Scryer already has, replacing its monitoring there, including a season selection. Movies are always added monitored.

Scryer has no anime movie catalog, so anime movies go to the default movie library unless you pick another one: in the server's settings, choose **Anime movies library** from the list of Scryer movie libraries the API key can add titles to (it needs **Manage titles** on that library). *Scryer's default movie library* leaves them where they are. The choice is kept by library id, so renaming the library in Scryer does not break it; if the library is later deleted, or Scryer refuses to list libraries, the request still goes through, to the default movie library. Scryer matches existing titles within one library, so an anime movie that is already in another movie library (the default one, say) is added again to the anime library and downloaded again.

## Setup

1. In Scryer, create an API key under your profile's **API keys**.
2. Install the plugin in Silo (**Admin → Plugins → Upload**) with the `plugin-linux-amd64` binary from a release.
3. Add a request server of type **Scryer** with Scryer's URL (for example `http://scryer:8080`, plus any base path) and the key, and run the connection test.
4. In Silo's request routing, send movies and series to the Scryer server.

## Build

```sh
make build      # ./plugin for this machine
make build-all  # dist/plugin-linux-{amd64,arm64}
make test
```
