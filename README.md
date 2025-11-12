MangaD 
=============================================

[![Go Report Card][go report card]][go report]

A shmol CLI manga downloader. Made to download manga from your liked websites as cbz files and read it offline.

Inspiration and Goal
--------

Insired by this [manga-downloader](https://github.com/elboletaire/manga-downloader). As good as it gets, eventually I wanted more customizability and generality, which is the goal of MangaD.

Of course it might not work as great on specific websites that are customly implemented in other loaders like the above mentioned one, but it should provide the user with the ability to download from most of the needed websites.

Usage
-------

The **mangad** executable should be run as:

~~~cmd
mangad <COMMAND> <Optional: sub-command> [Command flags + global flags in random order]
~~~

The execution must contain at least *1 command* and a maximum of *1 sub-command*.

**Commands**:

~~~cmd
config      Manage the config files for mangad donwload
download    Download the manga CBZ files with a specific configuration
completion  Generate the autocompletion script for the specified shell
help        Help about any command
version     Show the mangad version
~~~

---

**Global flags**:

~~~cmd
    --debug           Enable debug logging
-h, --help            Help for mangad. Can also be used on Commands. Interchangable with the `help` command
    --ignore-config   Ignore config and use only CLI flags
~~~

---

**Config** sub-commands:

~~~cmd
            If non is passed, the current non-empty values are printed
init        Create the Default config
add         Create a new config
list        List all available configs
reset       Reset the current config to default values
switch      Switch to a different configuration profile
edit        Edit current or a config by label (optional: <config_label>)
remove      Remove a config by label (<config_label>)
rename      Rename an existing labeled config (<old_label> <new_label>)
~~~

e.g. `mangad config init` or `mangad config switch Test`

---

**Completion** sub-commands:

~~~cmd
bash        Generate the autocompletion script for bash
fish        Generate the autocompletion script for fish
powershell  Generate the autocompletion script for powershell
zsh         Generate the autocompletion script for zsh
~~~

e.g. `mangad completion zsh`

----------------------------------------------------------

**Donwload** flags:

~~~cmd
--url string             Required. manga series (not chapter) page URL

--chapter       string   Download chapter by LABEL (e.g. 5 or 28.5)
--range         string   Download range of chapters by INDEX (e.g. 5-12)
--exclude-range string   Exclude range of chapters by INDEX (e.g. 5-12)
--list          string   Download specific chapter INDICES (e.g. 1,3,5)
--exclude-list  string   Exclude specific chapter INDICES (e.g. 1,3,5)
--allow-ext string       Allowed image extensions (e.g. "webp|jpg|png")

--output string          Output folder for CBZ files

--dry-run                Show what would be downloaded, don’t actually download
--chapter-workers int    Amount of parallel chapters to download (default 2)
--image-workers   int    Amount of parallel images to download per chapter (default 5)
--check-js               Tries a generic JS scanning & dynamic AJAX endpoint discovery
                         try if the images are loaded in a post-load script
--with-cf                Allow using embedded Selenium fallback when Cloudflare blocks requests
                         requires a working 'python3' executable with SeleniumBase installed

--keep-folders           Keep temporary folders with images that were used for CBZ conversion
--skip-broken            Skip failed images instead of failing the whole chapter

--cookie      string     Cookie string, e.g. "key=value; other=123"
--cookie-file string     Path to a text file with cookies (one header line)
--user-agent  string     Override User-Agent
~~~

e.g. `mangad download --url https://www.gachiakuta.net/ --range 1-20 --exclude-list 4,9,18`

The LABEL and INDEX in the `range`/`list`/`chapter` flags are reffering to the indeces of chapters in the array of all found chapters and numerical label of a chapter found on the website respectfully.

E.g. having 2 chapters with the buttons "Ch. 140" and "Ch. 140-2", to download the "Ch. 140-2" we could either add `--chapter 140-2` or `--list 2` (considering those are the only 2 chapters).

The `chapter` flag will be improved in the future to actually allow full names of chapters, not only numerical parts.

----

**version** command doesn't have any specific flags or sub-commands. Just prints out the version.

-----

Config
------

As you could see, there are quite a bunch of config sub-commands.

That’s because, in my humble (and definitely not biased) opinion, managing configuration parameters through the CLI is both inefficient and unnecessarily painful.

All the config files are by default `.yaml` and are saved into:
- Linux/Mac: either `XDG_CONFIG_HOME/mangad/configs/` or `~/.config/mangad/configs/`
- Windows: `APPDATA\mangad\configs`

`config init` also creates a `current_config` file in the `/mangad` directory, together with the `configs/Default.yaml`.

After this is done, we can `switch` between configs, fully eliminating the CLI flags, which is useful when downloading from the same website multiple times over.

Also helps with history to not spam that `up` key in searh of the needed command.

Download --with-cf and --check-js flags
-----

These are the fun ones.

`--check-js` came into existance after stumbling on some websites that would post-load the image urls after the initial HTML response from AJAX scripts.

It enables scanning all <script> tags for patterns like `fetch("/ajax/...")` or `axios.get("/api/...")`, and then probes those endpoints automatically.

It won’t handle obfuscated, dynamically built, or event-triggered scripts.

---

`--with-cg` solves the CF guard on the protected pages.

It is run with an embedded `python` script cause I couldn't be bothered (for now at least) to roll a proper solver in `go`.

Inherently, to be able to use the feature you'll need a `python3` executable in your path and the `SeleniumBase` lib installed (`pip install seleniumbase`).

In the future the plan is to add a `--force-cf` flag that will basically provide a much more robust `--check-js` behaviour by actually getting all the post-load scripts executed.

Contributing
---

PRs are always welcome :)

Unless it's absolutely necessary, I want to avoid website-specific implementations, so those have a higher chance of being turned down.



[go report]: https://goreportcard.com/report/github.com/brogergvhs/mangad
[go report card]: https://goreportcard.com/badge/github.com/brogergvhs/mangad
