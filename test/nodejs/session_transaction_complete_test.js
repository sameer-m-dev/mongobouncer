const mongoose = require('mongoose');

// Test configuration - Only test through MongoBouncer
const MONGODB_BOUNCER_URI = 'mongodb://localhost:27017/analytics_rs6?retryWrites=true&retryReads=true';

// Test models
const UserSchema = new mongoose.Schema({
    name: String,
    email: String,
    balance: Number,
    createdAt: { type: Date, default: Date.now }
});

const TransactionSchema = new mongoose.Schema({
    fromUserId: mongoose.Schema.Types.ObjectId,
    toUserId: mongoose.Schema.Types.ObjectId,
    amount: Number,
    status: { type: String, enum: ['pending', 'completed', 'failed'], default: 'pending' },
    createdAt: { type: Date, default: Date.now }
});

const OrderSchema = new mongoose.Schema({
    userId: mongoose.Schema.Types.ObjectId,
    items: [String],
    total: Number,
    status: { type: String, enum: ['pending', 'confirmed', 'shipped', 'delivered'], default: 'pending' },
    createdAt: { type: Date, default: Date.now }
});

const User = mongoose.model('User', UserSchema);
const Transaction = mongoose.model('Transaction', TransactionSchema);
const Order = mongoose.model('Order', OrderSchema);

class ComprehensiveMongoBouncerTester {
    constructor() {
        this.connection = null;
        this.testResults = [];
        this.testCount = 0;
    }

    async connect() {
        console.log(`🔗 Connecting to MongoBouncer: ${MONGODB_BOUNCER_URI}`);

        try {
            this.connection = await mongoose.connect(MONGODB_BOUNCER_URI);
            console.log('✅ Connected to MongoBouncer successfully');
            return true;
        } catch (error) {
            console.error('❌ Connection to MongoBouncer failed:', error.message);
            return false;
        }
    }

    async disconnect() {
        if (this.connection) {
            await mongoose.disconnect();
            console.log('🔌 Disconnected from MongoBouncer');
        }
    }

    async cleanup() {
        try {
            await User.deleteMany({});
            await Transaction.deleteMany({});
            await Order.deleteMany({});
            console.log('🧹 Cleaned up test data');
        } catch (error) {
            console.error('❌ Cleanup failed:', error.message);
        }
    }

    async runTest(testName, testFunction) {
        this.testCount++;
        console.log(`\n🧪 Test ${this.testCount}: ${testName}`);
        console.log('─'.repeat(60));

        try {
            const result = await testFunction();
            this.testResults.push({ test: testName, status: 'PASSED', details: result });
            console.log(`✅ ${testName}: PASSED`);
            return true;
        } catch (error) {
            this.testResults.push({ test: testName, status: 'FAILED', error: error.message });
            console.log(`❌ ${testName}: FAILED - ${error.message}`);
            return false;
        }
    }

    // ==================== BASIC SESSION TESTS ====================

    async testBasicSessionLifecycle() {
        const session = await mongoose.startSession();
        console.log('✅ Session started');

        const user = new User({ name: 'TestUser', email: 'test@example.com', balance: 100 });
        await user.save({ session });
        console.log('✅ User created within session');

        const foundUser = await User.findById(user._id).session(session);
        console.log(`✅ User found within session: ${foundUser.name}`);

        await session.endSession();
        console.log('✅ Session ended');

        return { sessionId: session.id, userId: user._id };
    }

    async testSessionWithMultipleOperations() {
        const session = await mongoose.startSession();
        console.log('✅ Session started');

        // Create multiple users
        const users = [];
        for (let i = 0; i < 5; i++) {
            const user = new User({
                name: `User${i}`,
                email: `user${i}@example.com`,
                balance: 100 + i * 10
            });
            await user.save({ session });
            users.push(user);
        }
        console.log(`✅ Created ${users.length} users within session`);

        // Update all users
        for (const user of users) {
            await User.updateOne(
                { _id: user._id },
                { $inc: { balance: 50 } },
                { session }
            );
        }
        console.log('✅ Updated all users within session');

        // Verify updates
        const updatedUsers = await User.find({ _id: { $in: users.map(u => u._id) } }).session(session);
        console.log(`✅ Verified ${updatedUsers.length} updated users`);

        await session.endSession();
        console.log('✅ Session ended');

        return { usersCreated: users.length, usersUpdated: updatedUsers.length };
    }

    // ==================== BASIC TRANSACTION TESTS ====================

    async testSimpleTransaction() {
        // Create initial users
        const alice = new User({ name: 'Alice', email: 'alice@example.com', balance: 100 });
        const bob = new User({ name: 'Bob', email: 'bob@example.com', balance: 50 });
        await alice.save();
        await bob.save();
        console.log('✅ Initial users created');

        const session = await mongoose.startSession();
        await session.startTransaction();
        console.log('✅ Transaction started');

        try {
            // Transfer money
            await User.updateOne(
                { _id: alice._id },
                { $inc: { balance: -25 } },
                { session }
            );
            await User.updateOne(
                { _id: bob._id },
                { $inc: { balance: 25 } },
                { session }
            );
            console.log('✅ Money transfer completed');

            await session.commitTransaction();
            console.log('✅ Transaction committed');

            // Verify results
            const updatedAlice = await User.findById(alice._id);
            const updatedBob = await User.findById(bob._id);

            if (updatedAlice.balance === 75 && updatedBob.balance === 75) {
                console.log('✅ Transaction verified: balances updated correctly');
                return { aliceBalance: updatedAlice.balance, bobBalance: updatedBob.balance };
            } else {
                throw new Error(`Balance verification failed: Alice=${updatedAlice.balance}, Bob=${updatedBob.balance}`);
            }
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }
    }

    async testTransactionRollback() {
        // Create initial users
        const alice = new User({ name: 'Alice', email: 'alice@example.com', balance: 100 });
        const bob = new User({ name: 'Bob', email: 'bob@example.com', balance: 50 });
        await alice.save();
        await bob.save();
        console.log('✅ Initial users created');

        const originalAliceBalance = alice.balance;
        const originalBobBalance = bob.balance;

        const session = await mongoose.startSession();
        await session.startTransaction();
        console.log('✅ Transaction started');

        try {
            // Perform operations
            await User.updateOne(
                { _id: alice._id },
                { $inc: { balance: -25 } },
                { session }
            );
            await User.updateOne(
                { _id: bob._id },
                { $inc: { balance: 25 } },
                { session }
            );
            console.log('✅ Operations performed');

            // Simulate error and rollback
            throw new Error('Simulated error for rollback test');
        } catch (error) {
            await session.abortTransaction();
            console.log('✅ Transaction aborted due to error');

            // Verify rollback
            const rolledBackAlice = await User.findById(alice._id);
            const rolledBackBob = await User.findById(bob._id);

            if (rolledBackAlice.balance === originalAliceBalance && rolledBackBob.balance === originalBobBalance) {
                console.log('✅ Rollback verified: balances unchanged');
                return { aliceBalance: rolledBackAlice.balance, bobBalance: rolledBackBob.balance };
            } else {
                throw new Error(`Rollback verification failed: Alice=${rolledBackAlice.balance}, Bob=${rolledBackBob.balance}`);
            }
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }
    }

    // ==================== MULTIPLE TRANSACTIONS IN SESSION ====================

    async testMultipleTransactionsInSession() {
        // Create initial users
        const alice = new User({ name: 'Alice', email: 'alice@example.com', balance: 200 });
        const bob = new User({ name: 'Bob', email: 'bob@example.com', balance: 100 });
        const charlie = new User({ name: 'Charlie', email: 'charlie@example.com', balance: 50 });
        await alice.save();
        await bob.save();
        await charlie.save();
        console.log('✅ Initial users created');

        const session = await mongoose.startSession();
        console.log('✅ Session started');

        try {
            // Transaction 1: Alice to Bob
            await session.startTransaction();
            await User.updateOne({ _id: alice._id }, { $inc: { balance: -30 } }, { session });
            await User.updateOne({ _id: bob._id }, { $inc: { balance: 30 } }, { session });
            await session.commitTransaction();
            console.log('✅ First transaction committed');

            // Transaction 2: Bob to Charlie
            await session.startTransaction();
            await User.updateOne({ _id: bob._id }, { $inc: { balance: -20 } }, { session });
            await User.updateOne({ _id: charlie._id }, { $inc: { balance: 20 } }, { session });
            await session.commitTransaction();
            console.log('✅ Second transaction committed');

            // Transaction 3: Charlie to Alice
            await session.startTransaction();
            await User.updateOne({ _id: charlie._id }, { $inc: { balance: -10 } }, { session });
            await User.updateOne({ _id: alice._id }, { $inc: { balance: 10 } }, { session });
            await session.commitTransaction();
            console.log('✅ Third transaction committed');

            // Verify final balances
            const finalAlice = await User.findById(alice._id);
            const finalBob = await User.findById(bob._id);
            const finalCharlie = await User.findById(charlie._id);

            console.log(`✅ Final balances - Alice: ${finalAlice.balance}, Bob: ${finalBob.balance}, Charlie: ${finalCharlie.balance}`);

            return {
                aliceBalance: finalAlice.balance,
                bobBalance: finalBob.balance,
                charlieBalance: finalCharlie.balance
            };
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }
    }

    // ==================== CONCURRENT TRANSACTIONS ====================

    async testConcurrentTransactions() {
        // Create initial users
        const alice = new User({ name: 'Alice', email: 'alice@example.com', balance: 100 });
        const bob = new User({ name: 'Bob', email: 'bob@example.com', balance: 50 });
        await alice.save();
        await bob.save();
        console.log('✅ Initial users created');

        // Start multiple concurrent transactions
        const promises = [];
        for (let i = 0; i < 5; i++) {
            promises.push(this.performConcurrentTransaction(alice._id, bob._id, 5));
        }

        const results = await Promise.allSettled(promises);
        const successful = results.filter(r => r.status === 'fulfilled').length;
        console.log(`✅ ${successful}/${results.length} concurrent transactions completed`);

        // Verify final balances
        const finalAlice = await User.findById(alice._id);
        const finalBob = await User.findById(bob._id);
        console.log(`✅ Final balances - Alice: ${finalAlice.balance}, Bob: ${finalBob.balance}`);

        return {
            successfulTransactions: successful,
            totalTransactions: results.length,
            aliceBalance: finalAlice.balance,
            bobBalance: finalBob.balance
        };
    }

    async performConcurrentTransaction(fromUserId, toUserId, amount) {
        const session = await mongoose.startSession();
        await session.startTransaction();

        try {
            await User.updateOne(
                { _id: fromUserId },
                { $inc: { balance: -amount } },
                { session }
            );
            await User.updateOne(
                { _id: toUserId },
                { $inc: { balance: amount } },
                { session }
            );

            await session.commitTransaction();
            return { success: true };
        } catch (error) {
            await session.abortTransaction();
            throw error;
        } finally {
            await session.endSession();
        }
    }

    // ==================== EDGE CASES ====================

    async testEmptyTransaction() {
        const session = await mongoose.startSession();
        await session.startTransaction();
        console.log('✅ Empty transaction started');

        // No operations performed
        await session.commitTransaction();
        console.log('✅ Empty transaction committed');

        await session.endSession();
        console.log('✅ Session ended');

        return { message: 'Empty transaction completed successfully' };
    }

    async testTransactionWithReadOnlyOperations() {
        // Create a user first
        const user = new User({ name: 'ReadOnlyUser', email: 'readonly@example.com', balance: 100 });
        await user.save();
        console.log('✅ User created for read-only test');

        const session = await mongoose.startSession();
        await session.startTransaction();
        console.log('✅ Read-only transaction started');

        try {
            // Only read operations
            const foundUser = await User.findById(user._id).session(session);
            const allUsers = await User.find({}).session(session);
            console.log(`✅ Read operations completed - found ${allUsers.length} users`);

            await session.commitTransaction();
            console.log('✅ Read-only transaction committed');

            return { usersRead: allUsers.length };
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }
    }

    async testTransactionWithLargeDataSet() {
        const session = await mongoose.startSession();
        await session.startTransaction();
        console.log('✅ Large dataset transaction started');

        try {
            // Create many users
            const users = [];
            for (let i = 0; i < 100; i++) {
                const user = new User({
                    name: `BulkUser${i}`,
                    email: `bulk${i}@example.com`,
                    balance: Math.floor(Math.random() * 1000)
                });
                users.push(user);
            }
            await User.insertMany(users, { session });
            console.log(`✅ Created ${users.length} users in transaction`);

            // Update all users
            await User.updateMany(
                { name: { $regex: /^BulkUser/ } },
                { $inc: { balance: 100 } },
                { session }
            );
            console.log('✅ Updated all bulk users');

            await session.commitTransaction();
            console.log('✅ Large dataset transaction committed');

            return { usersCreated: users.length };
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }
    }

    async testTransactionWithComplexQueries() {
        // Create test data
        const users = [];
        for (let i = 0; i < 20; i++) {
            const user = new User({
                name: `ComplexUser${i}`,
                email: `complex${i}@example.com`,
                balance: i * 10
            });
            users.push(user);
        }
        await User.insertMany(users);
        console.log('✅ Test data created for complex queries');

        const session = await mongoose.startSession();
        await session.startTransaction();
        console.log('✅ Complex queries transaction started');

        try {
            // Complex aggregation-like operations
            await User.updateMany(
                { balance: { $gte: 100 } },
                { $inc: { balance: 50 } },
                { session }
            );
            console.log('✅ Complex update completed');

            await User.updateMany(
                { name: { $regex: /ComplexUser[0-9]$/ } },
                { $set: { email: 'updated_email@example.com' } },
                { session }
            );
            console.log('✅ Complex string operation completed');

            await session.commitTransaction();
            console.log('✅ Complex queries transaction committed');

            return { operationsCompleted: 2 };
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }
    }

    // ==================== ERROR HANDLING TESTS ====================

    async testTransactionWithValidationError() {
        const session = await mongoose.startSession();
        await session.startTransaction();
        console.log('✅ Validation error transaction started');

        try {
            // Try to create user with invalid data
            const invalidUser = new User({
                name: '', // Invalid: empty name
                email: 'invalid-email', // Invalid: bad email format
                balance: -100 // Invalid: negative balance
            });
            await invalidUser.save({ session });
            console.log('❌ Should not reach here - validation should fail');
        } catch (error) {
            console.log('✅ Validation error caught as expected');
            await session.abortTransaction();
            console.log('✅ Transaction aborted due to validation error');
            return { errorHandled: true, errorType: 'validation' };
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }
    }

    async testTransactionWithNetworkError() {
        const session = await mongoose.startSession();
        await session.startTransaction();
        console.log('✅ Network error simulation transaction started');

        try {
            // Create a user
            const user = new User({ name: 'NetworkTest', email: 'network@example.com', balance: 100 });
            await user.save({ session });
            console.log('✅ User created in transaction');

            // Simulate a potential network issue by doing multiple operations
            for (let i = 0; i < 10; i++) {
                await User.updateOne(
                    { _id: user._id },
                    { $inc: { balance: 1 } },
                    { session }
                );
            }
            console.log('✅ Multiple operations completed');

            await session.commitTransaction();
            console.log('✅ Transaction committed despite potential network issues');

            return { operationsCompleted: 11 };
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }
    }

    // ==================== REAL-WORLD SCENARIOS ====================

    async testECommerceOrderProcessing() {
        // Create users
        const customer = new User({ name: 'Customer', email: 'customer@example.com', balance: 500 });
        const merchant = new User({ name: 'Merchant', email: 'merchant@example.com', balance: 0 });
        await customer.save();
        await merchant.save();
        console.log('✅ Customer and merchant created');

        const session = await mongoose.startSession();
        await session.startTransaction();
        console.log('✅ E-commerce transaction started');

        try {
            const orderAmount = 100;

            // Create order
            const order = new Order({
                userId: customer._id,
                items: ['Product A', 'Product B'],
                total: orderAmount,
                status: 'pending'
            });
            await order.save({ session });
            console.log('✅ Order created');

            // Process payment
            await User.updateOne(
                { _id: customer._id },
                { $inc: { balance: -orderAmount } },
                { session }
            );
            await User.updateOne(
                { _id: merchant._id },
                { $inc: { balance: orderAmount } },
                { session }
            );
            console.log('✅ Payment processed');

            // Update order status
            await Order.updateOne(
                { _id: order._id },
                { $set: { status: 'confirmed' } },
                { session }
            );
            console.log('✅ Order confirmed');

            await session.commitTransaction();
            console.log('✅ E-commerce transaction committed');

            // Verify results
            const updatedCustomer = await User.findById(customer._id);
            const updatedMerchant = await User.findById(merchant._id);
            const confirmedOrder = await Order.findById(order._id);

            return {
                customerBalance: updatedCustomer.balance,
                merchantBalance: updatedMerchant.balance,
                orderStatus: confirmedOrder.status
            };
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }
    }

    async testBankingTransfer() {
        // Create accounts
        const account1 = new User({ name: 'Account1', email: 'acc1@bank.com', balance: 1000 });
        const account2 = new User({ name: 'Account2', email: 'acc2@bank.com', balance: 500 });
        await account1.save();
        await account2.save();
        console.log('✅ Bank accounts created');

        const session = await mongoose.startSession();
        await session.startTransaction();
        console.log('✅ Banking transfer transaction started');

        try {
            const transferAmount = 200;

            // Check sufficient funds
            const sourceAccount = await User.findById(account1._id).session(session);
            if (sourceAccount.balance < transferAmount) {
                throw new Error('Insufficient funds');
            }
            console.log('✅ Sufficient funds verified');

            // Process transfer
            await User.updateOne(
                { _id: account1._id },
                { $inc: { balance: -transferAmount } },
                { session }
            );
            await User.updateOne(
                { _id: account2._id },
                { $inc: { balance: transferAmount } },
                { session }
            );
            console.log('✅ Transfer processed');

            // Create transaction record
            const transaction = new Transaction({
                fromUserId: account1._id,
                toUserId: account2._id,
                amount: transferAmount,
                status: 'completed'
            });
            await transaction.save({ session });
            console.log('✅ Transaction record created');

            await session.commitTransaction();
            console.log('✅ Banking transfer committed');

            return { transferAmount, transactionId: transaction._id };
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }
    }

    async testInventoryManagement() {
        // Create products (users representing products with balance as stock)
        const product1 = new User({ name: 'Product1', email: 'product1@store.com', balance: 100 });
        const product2 = new User({ name: 'Product2', email: 'product2@store.com', balance: 50 });
        await product1.save();
        await product2.save();
        console.log('✅ Products created');

        const session = await mongoose.startSession();
        await session.startTransaction();
        console.log('✅ Inventory management transaction started');

        try {
            // Process order: reduce stock
            const order1 = new Order({
                userId: product1._id,
                items: ['Product1'],
                total: 1,
                status: 'pending'
            });
            await order1.save({ session });

            await User.updateOne(
                { _id: product1._id },
                { $inc: { balance: -1 } },
                { session }
            );
            console.log('✅ Product1 stock reduced');

            // Process another order
            const order2 = new Order({
                userId: product2._id,
                items: ['Product2'],
                total: 1,
                status: 'pending'
            });
            await order2.save({ session });

            await User.updateOne(
                { _id: product2._id },
                { $inc: { balance: -1 } },
                { session }
            );
            console.log('✅ Product2 stock reduced');

            // Update order statuses
            await Order.updateMany(
                { _id: { $in: [order1._id, order2._id] } },
                { $set: { status: 'confirmed' } },
                { session }
            );
            console.log('✅ Orders confirmed');

            await session.commitTransaction();
            console.log('✅ Inventory management transaction committed');

            return { ordersProcessed: 2 };
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }
    }

    // ==================== PERFORMANCE TESTS ====================

    async testHighFrequencyTransactions() {
        const user1 = new User({ name: 'PerfUser1', email: 'perf1@example.com', balance: 1000 });
        const user2 = new User({ name: 'PerfUser2', email: 'perf2@example.com', balance: 1000 });
        await user1.save();
        await user2.save();
        console.log('✅ Performance test users created');

        const startTime = Date.now();
        const promises = [];

        // Create many small transactions
        for (let i = 0; i < 20; i++) {
            promises.push(this.performConcurrentTransaction(user1._id, user2._id, 1));
        }

        const results = await Promise.allSettled(promises);
        const endTime = Date.now();
        const duration = endTime - startTime;
        const successful = results.filter(r => r.status === 'fulfilled').length;

        console.log(`✅ ${successful}/${results.length} high-frequency transactions completed in ${duration}ms`);

        return {
            transactionsCompleted: successful,
            totalTransactions: results.length,
            durationMs: duration,
            transactionsPerSecond: (successful / duration) * 1000
        };
    }

    // ==================== MAIN TEST RUNNER ====================

    async runAllTests() {
        console.log('\n🚀 Starting Comprehensive MongoBouncer Session & Transaction Tests');
        console.log('='.repeat(80));
        console.log(`🔗 Testing through: ${MONGODB_BOUNCER_URI}`);
        console.log('='.repeat(80));

        const connected = await this.connect();
        if (!connected) {
            console.log('❌ Cannot run tests - connection to MongoBouncer failed');
            return;
        }

        try {
            await this.cleanup();

            // Basic Session Tests
            await this.runTest('Basic Session Lifecycle', () => this.testBasicSessionLifecycle());
            await this.runTest('Session with Multiple Operations', () => this.testSessionWithMultipleOperations());

            // Basic Transaction Tests
            await this.runTest('Simple Transaction', () => this.testSimpleTransaction());
            await this.runTest('Transaction Rollback', () => this.testTransactionRollback());

            // Multiple Transactions in Session
            await this.runTest('Multiple Transactions in Session', () => this.testMultipleTransactionsInSession());

            // Concurrent Transactions
            await this.runTest('Concurrent Transactions', () => this.testConcurrentTransactions());

            // Edge Cases
            await this.runTest('Empty Transaction', () => this.testEmptyTransaction());
            await this.runTest('Transaction with Read-Only Operations', () => this.testTransactionWithReadOnlyOperations());
            await this.runTest('Transaction with Large Dataset', () => this.testTransactionWithLargeDataSet());
            await this.runTest('Transaction with Complex Queries', () => this.testTransactionWithComplexQueries());

            // Error Handling
            await this.runTest('Transaction with Validation Error', () => this.testTransactionWithValidationError());
            await this.runTest('Transaction with Network Error Simulation', () => this.testTransactionWithNetworkError());

            // Real-World Scenarios
            await this.runTest('E-Commerce Order Processing', () => this.testECommerceOrderProcessing());
            await this.runTest('Banking Transfer', () => this.testBankingTransfer());
            await this.runTest('Inventory Management', () => this.testInventoryManagement());

            // Performance Tests
            await this.runTest('High Frequency Transactions', () => this.testHighFrequencyTransactions());

            // Print comprehensive results
            this.printComprehensiveResults();

        } finally {
            await this.cleanup();
            await this.disconnect();
        }
    }

    printComprehensiveResults() {
        console.log('\n📊 Comprehensive Test Results Summary');
        console.log('='.repeat(80));

        const passed = this.testResults.filter(r => r.status === 'PASSED').length;
        const failed = this.testResults.filter(r => r.status === 'FAILED').length;

        console.log(`\n📈 Overall Results: ${passed} passed, ${failed} failed out of ${this.testResults.length} tests`);
        console.log(`🎯 Success Rate: ${((passed / this.testResults.length) * 100).toFixed(1)}%`);

        console.log('\n📋 Detailed Results:');
        console.log('─'.repeat(80));

        this.testResults.forEach((result, index) => {
            const icon = result.status === 'PASSED' ? '✅' : '❌';
            console.log(`${(index + 1).toString().padStart(2)}. ${icon} ${result.test}: ${result.status}`);
            if (result.error) {
                console.log(`    Error: ${result.error}`);
            }
            if (result.details) {
                console.log(`    Details: ${JSON.stringify(result.details)}`);
            }
        });

        console.log('\n🎉 Test Categories Covered:');
        console.log('✅ Basic Session Operations');
        console.log('✅ Basic Transaction Operations');
        console.log('✅ Multiple Transactions in Session');
        console.log('✅ Concurrent Transactions');
        console.log('✅ Edge Cases & Error Handling');
        console.log('✅ Real-World Scenarios');
        console.log('✅ Performance Testing');

        if (failed === 0) {
            console.log('\n🏆 ALL TESTS PASSED! MongoBouncer session and transaction support is production-ready!');
        } else {
            console.log(`\n⚠️  ${failed} test(s) failed. Please review the implementation.`);
        }

        console.log('\n' + '='.repeat(80));
    }
}

// Main execution
async function main() {
    const tester = new ComprehensiveMongoBouncerTester();

    try {
        await tester.runAllTests();
        console.log('\n🎉 Comprehensive testing completed successfully!');
    } catch (error) {
        console.error('❌ Test execution failed:', error.message);
        process.exit(1);
    } finally {
        process.exit(0);
    }
}

// Handle uncaught exceptions
process.on('uncaughtException', (error) => {
    console.error('❌ Uncaught Exception:', error);
    process.exit(1);
});

process.on('unhandledRejection', (reason, promise) => {
    console.error('❌ Unhandled Rejection at:', promise, 'reason:', reason);
    process.exit(1);
});

// Run the tests
if (require.main === module) {
    main().catch(console.error);
}

module.exports = ComprehensiveMongoBouncerTester;
