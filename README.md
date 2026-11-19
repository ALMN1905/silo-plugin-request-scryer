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

Titles can be tagged `silo-request` and `silo-user-<username>` in Scryer (switch **Tag titles with the requester**, off until chosen), so Silo requests can be filtered and managed together. Scryer refuses a tag that is not defined, so the plugin defines missing tags first; a tag it is not allowed to define (the key needs the catalog-settings permission) is left off, the other tags are still used, and the request says which was left off. If Scryer can't be reached while tags are checked, Silo retries the request rather than adding the title untagged.

The user tag names the Silo **account**, not the profile: every profile on one account shares it. It keeps to lowercase letters, digits and dashes (`Ana Maria` becomes `silo-user-ana-maria`). A username that is an email address is cut at the `@`, so no address reaches Scryer, and a name with nothing usable left becomes the account's id (`silo-user-7`). Names that differ only in punctuation share a tag, and renaming an account starts a new tag while older titles keep the old one, so check this before pointing a Scryer maintenance rule at a user tag. Titles Scryer already had are left untouched unless **Also tag titles Scryer already had** is on (tags can drive Scryer's maintenance rules, so this is opt-in); `addTitle` ignores tags for an existing title, so the plugin then follows up with `updateTitleTags`.

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
