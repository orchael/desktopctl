# Issue 363: park and unpark EFS desktops

- [x] Capture and validate a versioned launch specification before resource deletion.
- [x] Add exclusive lifecycle transitions and retain the EFS workspace claim.
- [x] Add park preview, confirmation, destroy reconciliation, and volume deletion check.
- [x] Add restore, EFS identity check, host readiness, and retry paths.
- [x] Update PRD and operator documentation.
- [x] Run Go tests, build, and lint locally.
- [ ] Run the documented live AWS validation in a disposable environment.

Live validation is separate because it provisions an EC2 instance, root EBS
volume, and EFS workspace. The procedure is in `docs/PARKING.md`.
