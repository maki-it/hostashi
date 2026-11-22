// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import "testing"

func TestPythonAppSpec(t *testing.T) {
	base := AppSpec{Site: "shop", Python: "3.13", Workdir: "htdocs", StartKind: AppStartWSGI, Start: "shop.wsgi:application", Port: 20003}

	ok := []func(*AppSpec){
		func(*AppSpec) {},
		func(a *AppSpec) { a.StartKind, a.Start = AppStartASGI, "main:app" },
		func(a *AppSpec) { a.StartKind, a.Start = AppStartScript, "bot/run.py" },
		func(a *AppSpec) { a.Args = []string{"--workers", "3", "--timeout=60"} },
		func(a *AppSpec) { a.StartKind, a.Start, a.Args = AppStartScript, "run.py", []string{"--port", "1"} },
		func(a *AppSpec) { a.StartKind, a.Start, a.Stopped = "", "", true },
	}
	for i, f := range ok {
		a := base
		f(&a)

		if err := a.Validate(); err != nil {
			t.Errorf("ok %d: %v", i, err)
		}
	}

	bad := []func(*AppSpec){
		func(a *AppSpec) { a.Python = "2.7" },
		func(a *AppSpec) { a.Node = "24" },
		func(a *AppSpec) { a.Start = "shop.wsgi" },
		func(a *AppSpec) { a.Start = "shop.wsgi:application()" },
		func(a *AppSpec) { a.Start = "1shop:app" },
		func(a *AppSpec) { a.Start = "shop:app;rm" },
		func(a *AppSpec) { a.StartKind = AppStartNPM },
		func(a *AppSpec) { a.StartKind = AppStartBinary },
		func(a *AppSpec) { a.StartKind, a.Start = AppStartScript, "run.js" },
		func(a *AppSpec) { a.StartKind, a.Start = AppStartScript, "../run.py" },
		func(a *AppSpec) { a.Args = []string{"-b", "0.0.0.0:80"} },
		func(a *AppSpec) { a.Args = []string{"-b0.0.0.0:80"} },
		func(a *AppSpec) { a.Args = []string{"--bind=0.0.0.0:80"} },
		func(a *AppSpec) {
			a.StartKind, a.Start, a.Args = AppStartASGI, "main:app", []string{"--host", "0.0.0.0"}
		},
		func(a *AppSpec) { a.StartKind, a.Start, a.Args = AppStartASGI, "main:app", []string{"--port=80"} },
		func(a *AppSpec) { a.StartKind, a.Start, a.Args = AppStartASGI, "main:app", []string{"--uds", "/tmp/s"} },
		func(a *AppSpec) { a.StartKind, a.Start, a.Args = AppStartASGI, "main:app", []string{"--fd", "3"} },
		func(a *AppSpec) { a.StartKind, a.Start, a.Args, a.Stopped = "", "", []string{"x"}, true },
		func(a *AppSpec) { a.StartKind, a.Start = "", "" },
		func(a *AppSpec) { a.Args = []string{"a\nb"} },
	}
	for i, f := range bad {
		a := base
		f(&a)

		if err := a.Validate(); err == nil {
			t.Errorf("bad %d accepted: %+v", i, a)
		}
	}

	if !base.IsPython() || base.Binary() {
		t.Error("kind")
	}

	if err := (PythonVenvRequest{Site: "shop", Python: "3.13", Clear: true}).Validate(); err != nil {
		t.Error(err)
	}

	if err := (PythonVenvRequest{Site: "shop", Python: "3"}).Validate(); err == nil {
		t.Error("line 3 accepted")
	}
}
