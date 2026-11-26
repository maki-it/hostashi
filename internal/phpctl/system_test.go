// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import "testing"

func TestPGClientUnitName(t *testing.T) {
	got, err := pgClientUnit("19")
	if err != nil || got != "hostashi-pg-client-install@19.service" {
		t.Fatalf("19: %q %v", got, err)
	}

	for _, m := range []string{"", "13", "19.service", "19 ", "../19", "x"} {
		if _, err := pgClientUnit(m); err == nil {
			t.Errorf("%q accepted", m)
		}
	}
}

func TestAppUnitNames(t *testing.T) {
	ctx := t.Context()

	for _, u := range []string{"hostashi-node@.service", "hostashi-node@a.service", "hostashi-php-8.3@blog.service", "hostashi-node@blog.service.d", "hostashi-node@../x.service", "hostashi-app@.service", "hostashi-apps@blog.service", "hostashi-python-venv@blog.service", "hostashi-python@../x.service"} {
		if err := appctl(ctx, u, "start"); err == nil {
			t.Errorf("%q accepted", u)
		}
	}

	if _, err := (AppController{}).Show(ctx, "hostashi-node@blog.service", "ExecStart"); err == nil {
		t.Error("property ExecStart accepted")
	}

	for _, u := range []string{"hostashi-python@blog.service", "hostashi-node@blog.service", "hostashi-python-venv@.service", "hostashi-python-venv@blog.service.d"} {
		if err := (AppController{}).Run(ctx, u); err == nil {
			t.Errorf("run %q accepted", u)
		}
	}

	if !appUnitRe.MatchString("hostashi-python@blog.service") || !venvUnitRe.MatchString("hostashi-python-venv@blog.service") {
		t.Error("python units refused")
	}
}

func TestPHPRemoveUnitName(t *testing.T) {
	got, err := removeUnit("8.2")
	if err != nil || got != "hostashi-php-remove@8.2.service" {
		t.Fatalf("8.2: %q %v", got, err)
	}

	for _, v := range []string{"", "8.0", "8.2.service", "8.2 ", "../8.2"} {
		if _, err := removeUnit(v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

func TestPythonInstallUnitName(t *testing.T) {
	got, err := pythonInstallUnit("3.13")
	if err != nil || got != "hostashi-python-install@3.13.service" {
		t.Fatalf("3.13: %q %v", got, err)
	}

	for _, v := range []string{"", "3.10", "3", "3.13.service", "3.13 ", "../3.13", "x"} {
		if _, err := pythonInstallUnit(v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

func TestDeployUnitNames(t *testing.T) {
	ctx := t.Context()
	c := DeployController{}

	for _, u := range []string{"hostashi-deploy@.service", "hostashi-deploy@a.service", "hostashi-deploy@blog.service.d", "hostashi-deploy@../x.service", "hostashi-node@blog.service", "hostashi-deploys@blog.service", "hostashi-build@.service", "hostashi-builds@blog.service", "hostashi-build@../x.service"} {
		if err := c.StartNoBlock(ctx, u); err == nil {
			t.Errorf("%q accepted", u)
		}

		if _, err := c.Show(ctx, u, "ActiveState"); err == nil {
			t.Errorf("show %q accepted", u)
		}
	}

	if _, err := c.Show(ctx, "hostashi-deploy@blog.service", "ExecStart"); err == nil {
		t.Error("property ExecStart accepted")
	}

	if !deployUnitRe.MatchString("hostashi-deploy@blog.service") || !deployUnitRe.MatchString("hostashi-build@blog.service") {
		t.Error("deploy or build unit refused")
	}
}
