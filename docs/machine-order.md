# Arrange machines

The fleet's default **Name (A–Z)** order no longer changes when a machine goes
offline or its readings change. Alerts and status badges still show problems.

To choose your own positions:

1. On the fleet page, choose **Arrange machines**.
2. Drag a machine by its handle, or use the up/down arrows.
3. Choose **Save order**. The sort selector changes to **My order**.

Your order is saved to your BloxOS account and applies to grid/list views and
the Classic, Live Wall, Grove and Ops Console layouts. Each user has their own
order, including viewers. Saving requires a successful hub acknowledgement;
if the connection fails, the dialog keeps your draft so you can retry.

The arrangement includes all machines, even if you have a search or filter
active. Removed machines disappear, and newly added machines appear after the
saved set. Pins do not override **My order**. You can still explicitly choose
**Status**, **CPU %** or **GPU Temp** when you want a changing, live sort.

Upgrade both hub and dashboard to v1.2.0 or newer to save an arrangement.
The database migration adds an empty order without resetting existing users'
preferences. Existing explicitly selected live sorts remain selected; choose
**Name (A–Z)** or save **My order** for stable positions.
