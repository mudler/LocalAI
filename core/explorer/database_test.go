package explorer_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/explorer"
)

var _ = Describe("Database", func() {
	var (
		dbPath string
		db     *explorer.Database
		err    error
	)

	BeforeEach(func() {
		// Create a temporary file path for the database
		dbPath = "test_db.json"
		db, err = explorer.NewDatabase(dbPath)
		Expect(err).To(BeNil())
	})

	AfterEach(func() {
		// Clean up the temporary database file
		os.Remove(dbPath)
	})

	Context("when managing tokens", func() {
		It("should add and retrieve a token", func() {
			token := "token123"
			t := explorer.TokenData{Name: "TokenName", Description: "A test token"}

			err = db.Set(token, t)
			Expect(err).To(BeNil())

			retrievedToken, exists := db.Get(token)
			Expect(exists).To(BeTrue())
			Expect(retrievedToken).To(Equal(t))
		})

		It("should delete a token", func() {
			token := "token123"
			t := explorer.TokenData{Name: "TokenName", Description: "A test token"}

			err = db.Set(token, t)
			Expect(err).To(BeNil())

			err = db.Delete(token)
			Expect(err).To(BeNil())

			_, exists := db.Get(token)
			Expect(exists).To(BeFalse())
		})

		It("should persist data to disk", func() {
			token := "token123"
			t := explorer.TokenData{Name: "TokenName", Description: "A test token"}

			err = db.Set(token, t)
			Expect(err).To(BeNil())

			// Recreate the database object to simulate reloading from disk
			db, err = explorer.NewDatabase(dbPath)
			Expect(err).To(BeNil())

			retrievedToken, exists := db.Get(token)
			Expect(exists).To(BeTrue())
			Expect(retrievedToken).To(Equal(t))

			// Check the token list
			tokenList := db.TokenList()
			Expect(tokenList).To(ContainElement(token))
		})
	})

	Context("when loading an empty or non-existent file", func() {
		It("should start with an empty database", func() {
			dbPath = "empty_db.json"
			db, err = explorer.NewDatabase(dbPath)
			Expect(err).To(BeNil())

			_, exists := db.Get("nonexistent")
			Expect(exists).To(BeFalse())

			// Clean up
			os.Remove(dbPath)
		})
	})
})
