// Copyright (c) 2024 John Mylchreest <jmylchreest@gmail.com>
//
// This file is part of refind-btrfs-snapshots.
//
// refind-btrfs-snapshots is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// refind-btrfs-snapshots is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with refind-btrfs-snapshots. If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"os"

	"github.com/jmylchreest/refind-btrfs-snapshots/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
