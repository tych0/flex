package flex

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"gopkg.in/lxc/go-lxc.v2"
	"gopkg.in/tomb.v2"

	"github.com/kr/pty"
)

// A Daemon can respond to requests from a flex client.
type Daemon struct {
	tomb    tomb.Tomb
	config  Config
	unixl   net.Listener
	tcpl    net.Listener
	id_map  *idmap
	lxcpath string
	mux     *http.ServeMux
}

// varPath returns the provided path elements joined by a slash and
// appended to the end of $FLEX_DIR, which defaults to /var/lib/flex.
func varPath(path ...string) string {
	varDir := os.Getenv("FLEX_DIR")
	if varDir == "" {
		varDir = "/var/lib/flex"
	}
	items := []string{varDir}
	items = append(items, path...)
	return filepath.Join(items...)
}

// StartDaemon starts the flex daemon with the provided configuration.
func StartDaemon(config *Config) (*Daemon, error) {
	d := &Daemon{config: *config}
	d.mux = http.NewServeMux()
	d.mux.HandleFunc("/ping", d.servePing)
	d.mux.HandleFunc("/list", d.serveList)
	d.mux.HandleFunc("/create", d.serveCreate)
	d.mux.HandleFunc("/attach", d.serveAttach)
	d.mux.HandleFunc("/checkpoint", d.serveCheckpoint)
	d.mux.HandleFunc("/restore", d.serveRestore)
	d.mux.HandleFunc("/sendContainer", d.serveSendContainer)

	var err error
	d.id_map, err = newIdmap()
	if err != nil {
		return nil, err
	}
	Debugf("idmap is %d %d %d %d\n",
		d.id_map.uidmin,
		d.id_map.uidrange,
		d.id_map.gidmin,
		d.id_map.gidrange)

	d.mux.HandleFunc("/start", buildByNameServe("start", func(c *lxc.Container) error { return c.Start() }, d))
	d.mux.HandleFunc("/stop", buildByNameServe("stop", func(c *lxc.Container) error { return c.Stop() }, d))
	d.mux.HandleFunc("/reboot", buildByNameServe("reboot", func(c *lxc.Container) error { return c.Reboot() }, d))
	d.mux.HandleFunc("/destroy", buildByNameServe("destroy", func(c *lxc.Container) error { return c.Destroy() }, d))

	d.lxcpath = varPath("lxc")
	err = os.MkdirAll(varPath("/"), 0755)
	if err != nil {
		return nil, err
	}
	err = os.MkdirAll(d.lxcpath, 0755)
	if err != nil {
		return nil, err
	}

	unixAddr, err := net.ResolveUnixAddr("unix", varPath("unix.socket"))
	if err != nil {
		return nil, fmt.Errorf("cannot resolve unix socket address: %v", err)
	}
	unixl, err := net.ListenUnix("unix", unixAddr)
	if err != nil {
		return nil, fmt.Errorf("cannot listen on unix socket: %v", err)
	}
	d.unixl = unixl

	if d.config.ListenAddr != "" {
		// Watch out. Threre's a listener active which must be closed on errors.
		tcpAddr, err := net.ResolveTCPAddr("tcp", d.config.ListenAddr)
		if err != nil {
			d.unixl.Close()
			return nil, fmt.Errorf("cannot resolve unix socket address: %v", err)
		}
		tcpl, err := net.ListenTCP("tcp", tcpAddr)
		if err != nil {
			d.unixl.Close()
			return nil, fmt.Errorf("cannot listen on unix socket: %v", err)
		}
		d.tcpl = tcpl
		d.tomb.Go(func() error { return http.Serve(d.tcpl, d.mux) })
	}

	d.tomb.Go(func() error { return http.Serve(d.unixl, d.mux) })
	return d, nil
}

var errStop = fmt.Errorf("requested stop")

// Stop stops the flex daemon.
func (d *Daemon) Stop() error {
	d.tomb.Kill(errStop)
	d.unixl.Close()
	if d.tcpl != nil {
		d.tcpl.Close()
	}
	err := d.tomb.Wait()
	if err == errStop {
		return nil
	}
	return err
}

// None of the daemon methods should print anything to stdout or stderr. If
// there's a local issue in the daemon that the admin should know about, it
// should be logged using either Logf or Debugf.
//
// Then, all of those issues that prevent the request from being served properly
// for any reason (bad parameters or any other local error) should be notified
// back to the client by writing an error json document to w, which in turn will
// be read by the client and returned via the API as an error result. These
// errors then surface via the CLI (cmd/flex/*) in os.Stderr.
//
// Together, these ideas ensure that we have a proper daemon, and a proper client,
// which can both be used independently and also embedded into other applications.

func (d *Daemon) servePing(w http.ResponseWriter, r *http.Request) {
	remoteAddr := r.RemoteAddr
	if remoteAddr == "@" {
		remoteAddr = "unix socket"
	}
	Debugf("responding to ping from %s", remoteAddr)
	w.Write([]byte("pong"))
}

// FIXME(niemeyer): These methods should be returning json to the client.
// They may be easily converted by replacing:
//
//     fmt.Fprintf(w, "Port: %d", port)
//
// with:
//
//     type jmap map[string]interface{}
//     err := json.NewEncoder(w).Encode(jmap{"port": port})
//
// Common results may also be done with a struct. For example, for errors
// something like this might be convenient:
//
//     type jerror struct {
//         Error string `json:"error"`
//     }
//
// It may then be used as:
//
//     err := json.NewEncoder(w).Encode(jerror{"message"})
//
// I suggest establishing a few strong conventions early on for how an error
// document looks like, etc.

func (d *Daemon) serveList(w http.ResponseWriter, r *http.Request) {
	Debugf("responding to list")
	c := lxc.DefinedContainers(d.lxcpath)
	for i := range c {
		fmt.Fprintf(w, "%d: %s (%s)\n", i, c[i].Name(), c[i].State())
	}

}

func (d *Daemon) serveAttach(w http.ResponseWriter, r *http.Request) {
	Debugf("responding to attach")

	name := r.FormValue("name")
	if name == "" {
		fmt.Fprintf(w, "failed parsing name")
		return
	}

	command := r.FormValue("command")
	if command == "" {
		fmt.Fprintf(w, "failed parsing command")
		return
	}

	secret := r.FormValue("secret")
	if secret == "" {
		fmt.Fprintf(w, "failed parsing secret")
		return
	}

	addr := ":0"
	// tcp6 doesn't seem to work with Dial("tcp", ) at the client
	l, err := net.Listen("tcp4", addr)
	if err != nil {
		fmt.Fprintf(w, "failed listening")
		return
	}
	fmt.Fprintf(w, "%s", l.Addr().String())

	go func(l net.Listener, name string, command string, secret string) {
		conn, err := l.Accept()
		l.Close()
		if err != nil {
			Debugf(err.Error())
			return
		}
		defer conn.Close()

		// FIXME(niemeyer): This likely works okay because the kernel tends to
		// be sane enough to not break down such a small amount of data into
		// multiple operations. That said, if we were to make it work
		// independent of the good will of the kernel and network layers, we'd
		// have to take into account that Read might also return a single byte,
		// for example, and then return more when it was next called. Or, it
		// might return a password plus more data that the client delivered
		// anticipating it would have a successful authentication.
		//
		// We could easily handle it using buffered io (bufio package), but that
		// would spoil the use of conn directly below when binding it to
		// the pty. So, given it's a trivial amount of data, I suggest calling
		// a local helper function that will read byte by byte until it finds
		// a predefined delimiter ('\n'?) and returns (data string, err error).
		//
		b := make([]byte, 100)
		n, err := conn.Read(b)
		if err != nil {
			Debugf("bad read: %s", err.Error())
			return
		}
		if n != len(secret) {
			Debugf("read %d characters, secret is %d", n, len(secret))
			return
		}
		if string(b[:n]) != secret {
			Debugf("Wrong secret received from attach client")
			return
		}
		Debugf("Attaching")

		c, err := lxc.NewContainer(name, d.lxcpath)
		if err != nil {
			Debugf("%s", err.Error())
		}

		pty, tty, err := pty.Open()

		if err != nil {
			Debugf("Failed opening getting a tty: %q", err.Error())
			return
		}

		defer pty.Close()
		defer tty.Close()

		/*
		 * The pty will be passed to the container's Attach.  The two
		 * below goroutines will copy output from the socket to the
		 * pty.stdin, and from pty.std{out,err} to the socket
		 * If the RunCommand exits, we want ourselves (the gofunc) and
		 * the copy-goroutines to exit.  If the connection closes, we
		 * also want to exit
		 */
		go func() {
			io.Copy(pty, conn)
			Debugf("conn->pty exiting")
			return
		}()
		go func() {
			io.Copy(conn, pty)
			Debugf("pty->conn exiting")
			return
		}()

		options := lxc.DefaultAttachOptions

		options.StdinFd = tty.Fd()
		options.StdoutFd = tty.Fd()
		options.StderrFd = tty.Fd()

		options.ClearEnv = true

		_, err = c.RunCommand([]string{command}, options)
		if err != nil {
			return
		}

		Debugf("RunCommand exited, stopping console")
	}(l, name, command, secret)
}

func (d *Daemon) serveCreate(w http.ResponseWriter, r *http.Request) {
	Debugf("responding to create")

	name := r.FormValue("name")
	if name == "" {
		fmt.Fprintf(w, "failed parsing name")
		return
	}

	distro := r.FormValue("distro")
	if distro == "" {
		fmt.Fprintf(w, "failed parsing distro")
		return
	}

	release := r.FormValue("release")
	if release == "" {
		fmt.Fprintf(w, "failed parsing release")
		return
	}

	arch := r.FormValue("arch")
	if arch == "" {
		fmt.Fprintf(w, "failed parsing arch")
		return
	}

	opts := lxc.TemplateOptions{
		Template: "download",
		Distro:   distro,
		Release:  release,
		Arch:     arch,
	}

	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		return
	}

	/*
	 * Set the id mapping. This may not be how we want to do it, but it's a
	 * start.  First, we remove any id_map lines in the config which might
	 * have come from ~/.config/lxc/default.conf.  Then add id mapping based
	 * on Domain.id_map
	 */
	if d.id_map != nil {
		Debugf("setting custom idmap")
		err = c.SetConfigItem("lxc.id_map", "")
		if err != nil {
			fmt.Fprintf(w, "Failed to clear id mapping, continuing")
		}
		uidstr := fmt.Sprintf("u 0 %d %d\n", d.id_map.uidmin, d.id_map.uidrange)
		Debugf("uidstr is %s\n", uidstr)
		err = c.SetConfigItem("lxc.id_map", uidstr)
		if err != nil {
			fmt.Fprintf(w, "Failed to set uid mapping")
			return
		}
		gidstr := fmt.Sprintf("g 0 %d %d\n", d.id_map.gidmin, d.id_map.gidrange)
		err = c.SetConfigItem("lxc.id_map", gidstr)
		if err != nil {
			fmt.Fprintf(w, "Failed to set gid mapping")
			return
		}
		c.SaveConfigFile("/tmp/c")
	}

	/*
	 * Actually create the container
	 */
	err = c.Create(opts)
	if err != nil {
		fmt.Fprintf(w, "fail!")
	} else {
		fmt.Fprintf(w, "success!")
	}
}

type byname func(*lxc.Container) error

func buildByNameServe(function string, f byname, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		Debugf("responding to %s", function)

		name := r.FormValue("name")
		if name == "" {
			fmt.Fprintf(w, "failed parsing name")
			return
		}

		c, err := lxc.NewContainer(name, d.lxcpath)
		if err != nil {
			fmt.Fprintf(w, "failed getting container")
			return
		}

		err = f(c)
		if err != nil {
			fmt.Fprintf(w, "operation failed")
			return
		}
	}
}

func makeCheckpointPath(name string, id string) string {
	return varPath("checkpoints", name, id)
}

func (d *Daemon) serveCheckpoint(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if name == "" {
		fmt.Fprintf(w, "failed parsing name")
		return
	}

	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		fmt.Fprintf(w, "failed getting container")
		return
	}

	err = os.MkdirAll(varPath("checkpoints", name), 0700)
	if err != nil {
		fmt.Fprintf(w, "failed making checkpoint directory")
		return
	}

	path := ""
	id := -1
	/* We probably want to be a bit smarter here... */
	for i := 0; i < 1000; i++ {
		tmp := makeCheckpointPath(name, string(i))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			path = tmp
			id = i
			break
		}
	}

	if id < 0 {
		fmt.Fprintf(w, "Too many checkpoints?")
		return
	}

	err = os.MkdirAll(path, 0700)
	if err != nil {
		fmt.Fprintf(w, "Failed making checkpoint dir")
		return
	}

	stop := r.FormValue("stop") == ""
	verbose := r.FormValue("verbose") == ""


	err = c.Checkpoint(lxc.CheckpointOpts{path, stop, verbose})
	if err != nil {
		fmt.Fprintf(w, "Checkpoint failed")
		return
	}

	fmt.Fprintf(w, string(id))
}

func (d *Daemon) serveRestore(w http.ResponseWriter, r *http.Request) {

	name := r.FormValue("name")
	if name == "" {
		fmt.Fprintf(w, "failed parsing name")
		return
	}

	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		fmt.Fprintf(w, "failed getting container")
		return
	}

	id := r.FormValue("id")

	path := makeCheckpointPath(name, id)
	fi, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(w, "checkpoint doesn't exist!")
		return
	}

	if !fi.IsDir() {
		fmt.Fprintf(w, "checkpoint isn't a directory")
		return
	}

	verbose := r.FormValue("verbose") == ""

	err = c.Restore(lxc.RestoreOpts{path, verbose})
	if err != nil {
		fmt.Fprintf(w, "restore failed!")
		return
	}

	fmt.Fprintf(w, "restore success!")
}

/*
 * This probably isn't how we want to do this forever, because it requires
 * ssh keys be exchanged between the servers, which isn't ideal. I wasn't sure
 * of the best way to upload the rootfs/images to the remote, though, so I
 * used rsync for now.
 *
 * XXX: this also assumes that the flexpaths are the same on both this host
 * and the remote host; ideally we'd ask the remote where to sync to (or
 * better yet, just send the files and let the remote decide where to put
 * them).
 */

func doRsync(remote string, p string) error {
	/* rsync needs the trailing / to sync directories... */
	rsyncPath := p + "/"
	remotePath := remote + ":" + rsyncPath
	cmd := exec.Command("rsync", "-rltzha", "--devices", "--rsync-path=\"sudo rsync\"", rsyncPath, remotePath)

	return cmd.Run()
}

func (d *Daemon) serveSendContainer(w http.ResponseWriter, r *http.Request) {

	name := r.FormValue("name")
	if name == "" {
		fmt.Fprintf(w, "no name?")
		return
	}

	/* It is ok to not provide a checkpoint id, that just means you're
	 * doing an offline send. */
	checkpoint := r.FormValue("checkpoint")

	remote := r.FormValue("remote")
	if remote == "" {
		fmt.Fprintf(w, "error parsing remote")
		return
	}

	if checkpoint != "" {
		id := r.FormValue("id")

		path := makeCheckpointPath(name, id)
		fi, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(w, "checkpoint doesn't exist!")
			return
		}

		if !fi.IsDir() {
			fmt.Fprintf(w, "checkpoint isn't a directory")
			return
		}

		if doRsync(remote, path) != nil {
			fmt.Fprintf(w, "rsync of checkpoint failed!")
			return
		}
	}

	if doRsync(remote, filepath.Join(d.lxcpath, name)) != nil {
		fmt.Fprintf(w, "rsync of container failed!")
		return
	}

	fmt.Fprintf(w, "send successful!")
}
