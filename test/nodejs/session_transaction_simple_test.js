const mongoose = require('mongoose');

const MONGODB_BOUNCER_URI = 'mongodb://localhost:27017/analytics_rs6';

const UserSchema = new mongoose.Schema({
    name: String,
    email: String,
    balance: Number,
    createdAt: { type: Date, default: Date.now }
});

const User = mongoose.model('User', UserSchema);

async function testSimpleTransaction() {
    try {
        console.log('🔗 Connecting to MongoBouncer...');
        await mongoose.connect(MONGODB_BOUNCER_URI);
        console.log('✅ Connected successfully');

        // Clean up
        await User.deleteMany({});
        console.log('🧹 Cleaned up test data');

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

            console.log(`✅ Final balances - Alice: ${updatedAlice.balance}, Bob: ${updatedBob.balance}`);
        } finally {
            await session.endSession();
            console.log('✅ Session ended');
        }

    } catch (error) {
        console.error('❌ Test failed:', error.message);
    } finally {
        await mongoose.disconnect();
        console.log('🔌 Disconnected');
    }
}

testSimpleTransaction();
