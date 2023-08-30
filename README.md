# NV Fan Control

A small utility to control a PWM fan via sysfs based on Nvidia GPU temperature.

Hacked this up for a custom 12v blower fan I'm running on an old Nvidia Tesla P100, "it works on my machine" / YMMV / GLHF etc...

## Usage

```shell
nv_fan_control -help
nv_fan_control -sensitivity 2 -maxPWM 245 -basePWM 55 -interval 3 -fanpath /sys/class/hwmon/hwmon5/pwm3
```

## Installation

```shell
go build nv_fan_control.go
chmod +x nv_fan_control
mv nv_fan_control /usr/local/sbin/
```

_Note: I have added a install.sh script, but haven't tested it on a clean machine yet._

You may also want to create a systemd service to run this at boot and in the background.

```shell
cp nv_fan_control.service /etc/systemd/system/
systemctl daemon reload
systemctl enable nv_fan_control.service --now
```

## NCT 6775 + Fedora 38

My mainboard is a ASRock x670 Pro RS (_sorry, no turbo or intercooler_) which uses an NCT 6775 controller.

```shell
cat /etc/modprobe.d/am5-sensors.conf
options nct6775 force_id=0xd420 force
```

---

Bonus - To check for thermal throttling on Nvidia GPUs:

```shell
nvidia-smi -q -d PERFORMANCE
```
