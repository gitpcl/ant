package main

import "fmt"

// users loads every user then issues one query per user inside the loop — a
// classic 1+N query pattern. n+1-query flags this as a high-severity finding;
// it is the fixture the CI-mode test gates on (--fail-on=high → exit 1).
func users(ids []int) {
	for _, id := range ids {
		row := queryOne(id) // 1+N: a query per iteration
		fmt.Println(row)
	}
}

func queryOne(id int) string { return fmt.Sprintf("user-%d", id) }

func main() { users([]int{1, 2, 3}) }
