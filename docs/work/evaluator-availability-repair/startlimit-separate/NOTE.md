# Independent finding — NOT part of the :10122 evaluator repair

`~/.config/systemd/user/awg-awareness-graph.service` declares

    [Service]
    StartLimitBurst=5
    StartLimitIntervalSec=120

systemd ignores both there and says so on every unit load. Verified in the
journal rather than inferred from the manual:

    $ journalctl --user -u awg-awareness-graph.service | grep -i "unknown key"
    Aug 30 17:47:52 globule-ryzen systemd[1511]: /home/dave/.config/systemd/user/awg-awareness-graph.service:58:
      Unknown key name 'StartLimitIntervalSec' in section 'Service', ignoring.

So the retry bound that unit's own comment describes -- "five failures in two
minutes and it stops, so `systemctl --user status` shows a failed unit instead
of an eternally activating one" -- has never been enforced. The comment
documents an intent the file does not implement.

REPAIR (later, on its own):
  ~/.config/systemd/user/awg-awareness-graph.service.d/10-startlimit-in-unit.conf

    [Unit]
    StartLimitIntervalSec=120
    StartLimitBurst=5

DO NOT install this together with the :10122 evaluator repair. If both land at
once, a successful restart test cannot say which change produced the behaviour.
