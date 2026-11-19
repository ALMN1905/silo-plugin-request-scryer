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

A request is meant to end in a download, so what the plugin changes in Scryer is only what the request needs. A title Scryer already has keeps its settings (monitor type, specials), and episodes that already have a file or are already monitored are left as they are.

**Movies.** A new movie is added monitored. A movie Scryer already has is left alone when it has a file; without one, it is switched on so the request downloads.

**Series and anime** request the seasons Silo names. Anime series go to Scryer's anime library. A new title is added unmonitored and then each requested season is switched on whole; Scryer loads a new title's seasons a few seconds after it is added, so the plugin waits for them. In a title Scryer already has, only the episodes of the requested seasons that have no file and are not monitored are switched on, one by one. A season Scryer does not have (an upcoming one, say) is left out and named in the request's message, and the other seasons are requested all the same; with none of them in Scryer the request fails and changes nothing. The request counts as completed when every requested season Scryer has got files.

The plugin declares that it takes seasons (`supports_seasons`), so Silo lets a user request more seasons of a series that is already in its library.

A series that has not aired yet has no seasons to name; Silo then asks for the whole series. The plugin adds it monitored with *Missing and future episodes*: its aired episodes, if Scryer already has some (Scryer's data can be ahead of TMDB's), and everything to come. A title Scryer already has is looked up first and left as it is. Scryer monitors every episode of a title added without a choice (it does not apply the library's default monitoring to titles added through the API), which is why the plugin always sends one.

**Monitor specials of new series** monitors the specials of a new series (not anime), off by default. Scryer has no setting for the specials of a series: they are never monitored, unlike anime, which follow the anime specials setting in Scryer. The switch fills that gap, and only for new series; anime and titles Scryer already has keep what Scryer says. Silo cannot request specials, so this is the admin's decision.

**TMDB API key.** Silo numbers seasons as TMDB does, and a few titles (anime above all) are split or merged differently in Scryer. The plugin reads the series' seasons from TMDB with the API Read Access Token of a free themoviedb.org account (Settings, API; the shorter API Key is also accepted but has not been tried against TMDB) and matches each requested season to the Scryer seasons by air date and episode count, for example TMDB's one season of 24 episodes to Scryer's two of 12. A series request fails with a message when no key is set or TMDB refuses it; if TMDB cannot be reached, Silo retries the request.

Scryer has no anime movie catalog, so anime movies go to the default movie library unless you pick another one: in the server's settings, choose **Anime movies library** from the list of Scryer movie libraries the API key can add titles to (it needs **Manage titles** on that library). *Scryer's default movie library* leaves them where they are. The choice is kept by library id, so renaming the library in Scryer does not break it; if the library is later deleted, or Scryer refuses to list libraries, the request still goes through, to the default movie library. An anime movie that is already in another movie library is found there and not added again.

Titles can be tagged `silo-request` and `silo-user-<username>` in Scryer (switch **Tag titles with the requester**, off until chosen), so Silo requests can be filtered and managed together. Scryer refuses a tag that is not defined, so the plugin defines missing tags first; a tag it is not allowed to define (the key needs the catalog-settings permission) is left off, the other tags are still used, and the request says which was left off. If Scryer can't be reached while tags are checked, Silo retries the request rather than adding the title untagged.

The user tag names the Silo **account**, not the profile: every profile on one account shares it. It keeps to lowercase letters, digits and dashes (`Ana Maria` becomes `silo-user-ana-maria`). A username that is an email address is cut at the `@`, so no address reaches Scryer, and a name with nothing usable left becomes the account's id (`silo-user-7`). Names that differ only in punctuation share a tag, and renaming an account starts a new tag while older titles keep the old one, so check this before pointing a Scryer maintenance rule at a user tag. A new title is tagged when it is added; a title Scryer already has is tagged only when a request monitored something in it.

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
